package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"net"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const orderQueue = "orders"

// Broker publishes and consumes order events on a single HardhatQ process.
type Broker struct {
	conn *amqp.Connection
	pub  *amqp.Channel
	mu   sync.Mutex
}

func OpenBroker(url string, tlsConfig *tls.Config) (*Broker, error) {
	conn, err := amqp.DialConfig(url, amqp.Config{
		TLSClientConfig: tlsConfig,
		Dial: func(network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, 2*time.Second)
		},
	})
	if err != nil {
		return nil, err
	}
	pub, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := declareOrderQueue(pub); err != nil {
		pub.Close()
		conn.Close()
		return nil, err
	}
	return &Broker{conn: conn, pub: pub}, nil
}

func (b *Broker) Ping(context.Context) error {
	if b.conn.IsClosed() {
		return errors.New("connection closed")
	}
	ch, err := b.conn.Channel()
	if err != nil {
		return err
	}
	return ch.Close()
}

func (b *Broker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	pubErr := b.pub.Close()
	connErr := b.conn.Close()
	return errors.Join(pubErr, connErr)
}

// Publish sends the order JSON to the orders queue.
func (b *Broker) Publish(ctx context.Context, order Order) error {
	body, err := json.Marshal(order)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pub.PublishWithContext(ctx, "", orderQueue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         body,
	})
}

// Consume reads the orders queue until ctx is cancelled. handle runs for each
// delivery. A handler error requeues the message. A body that is not JSON is
// discarded.
func (b *Broker) Consume(ctx context.Context, handle func(context.Context, Order) error) error {
	ch, err := b.conn.Channel()
	if err != nil {
		return err
	}
	if err := declareOrderQueue(ch); err != nil {
		ch.Close()
		return err
	}
	msgs, err := ch.Consume(orderQueue, "", false, false, false, false, nil)
	if err != nil {
		ch.Close()
		return err
	}
	go func() {
		defer ch.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("hardhatq consumer stopped")
					return
				}
				var order Order
				if err := json.Unmarshal(d.Body, &order); err != nil {
					log.Printf("discard order message: %v", err)
					_ = d.Ack(false)
					continue
				}
				if err := handle(ctx, order); err != nil {
					log.Printf("order %d: %v", order.ID, err)
					_ = d.Nack(false, true)
					continue
				}
				_ = d.Ack(false)
			}
		}
	}()
	return nil
}

func declareOrderQueue(ch *amqp.Channel) error {
	_, err := ch.QueueDeclare(orderQueue, true, false, false, false, nil)
	return err
}
