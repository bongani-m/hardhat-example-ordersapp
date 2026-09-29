package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Cache stores order JSON in a single HardhatKV process.
type Cache struct {
	rdb *redis.Client
}

func OpenCache(ctx context.Context, addr, password string) (*Cache, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: password})
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, err
	}
	return &Cache{rdb: rdb}, nil
}

func (c *Cache) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *Cache) Close() error {
	return c.rdb.Close()
}

func (c *Cache) Get(ctx context.Context, id int64) (Order, bool, error) {
	raw, err := c.rdb.Get(ctx, orderKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Order{}, false, nil
	}
	if err != nil {
		return Order{}, false, err
	}
	var order Order
	if err := json.Unmarshal(raw, &order); err != nil {
		return Order{}, false, err
	}
	return order, true, nil
}

func (c *Cache) Set(ctx context.Context, order Order) error {
	raw, err := json.Marshal(order)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, orderKey(order.ID), raw, 0).Err()
}

func orderKey(id int64) string {
	return fmt.Sprintf("order:%d", id)
}
