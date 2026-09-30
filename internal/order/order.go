package order

import "time"

// Order is a row in shop.orders. HardhatDB is the source of truth.
type Order struct {
	ID        int64     `json:"id"`
	Item      string    `json:"item"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
