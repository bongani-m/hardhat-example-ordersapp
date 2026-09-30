package broker

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

	"github.com/bongani-m/hardhat-example-ordersapp/internal/order"
)

const (
	orderQueue    = "orders"
	handleTimeout = 15 * time.Second
)

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
func (b *Broker) Publish(ctx context.Context, row order.Order) error {
	body, err := json.Marshal(row)
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

// Consume reads the orders queue until ctx is cancelled or the delivery
// channel closes. It blocks. Prefetch is 1, so one message is in flight.
// A handler error requeues the message. A body that is not JSON is discarded.
// On cancel, the in-flight handler finishes before Consume returns.
func (b *Broker) Consume(ctx context.Context, handle func(context.Context, order.Order) error) error {
	ch, err := b.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := declareOrderQueue(ch); err != nil {
		return err
	}
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	msgs, err := ch.Consume(orderQueue, "ordersapp", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-msgs:
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return errors.New("hardhatq consumer stopped")
			}
			handleDelivery(ctx, d, handle)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
}

func handleDelivery(ctx context.Context, d amqp.Delivery, handle func(context.Context, order.Order) error) {
	var row order.Order
	if err := json.Unmarshal(d.Body, &row); err != nil {
		log.Printf("discard order message: %v", err)
		_ = d.Ack(false)
		return
	}
	handleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handleTimeout)
	defer cancel()
	if err := handle(handleCtx, row); err != nil {
		log.Printf("order %d: %v", row.ID, err)
		_ = d.Nack(false, true)
		return
	}
	_ = d.Ack(false)
}

func declareOrderQueue(ch *amqp.Channel) error {
	_, err := ch.QueueDeclare(orderQueue, true, false, false, false, nil)
	return err
}
