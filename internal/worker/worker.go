package worker

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"time"

	"github.com/bongani-m/hardhat-example-ordersapp/internal/broker"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/cache"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/order"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/store"
)

// Run consumes the orders queue until ctx is cancelled. A dropped broker
// connection is redialed with backoff. Run returns nil when ctx is cancelled.
func Run(ctx context.Context, url string, tlsConfig *tls.Config, st *store.Store, c *cache.Cache) error {
	var delay time.Duration
	for {
		if ctx.Err() != nil {
			return nil
		}
		b, err := broker.OpenBroker(url, tlsConfig)
		if err != nil {
			log.Printf("hardhatq: %v", err)
			if !sleep(ctx, nextDelay(&delay)) {
				return nil
			}
			continue
		}
		delay = 0
		log.Printf("hardhatq consuming")
		err = b.Consume(ctx, func(ctx context.Context, row order.Order) error {
			if err := st.MarkDone(ctx, row.ID); err != nil {
				return err
			}
			updated, err := st.Get(ctx, row.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			return c.Set(ctx, updated)
		})
		_ = b.Close()
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("hardhatq: %v", err)
		if !sleep(ctx, nextDelay(&delay)) {
			return nil
		}
	}
}

func nextDelay(delay *time.Duration) time.Duration {
	if *delay <= 0 {
		*delay = time.Second
		return *delay
	}
	*delay *= 2
	if *delay > 30*time.Second {
		*delay = 30 * time.Second
	}
	return *delay
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
