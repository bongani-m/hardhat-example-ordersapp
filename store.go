package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Order is a row in shop.orders. HardhatDB is the source of truth.
type Order struct {
	ID        int64     `json:"id"`
	Item      string    `json:"item"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrNotFound is returned when an order id is not in HardhatDB.
var ErrNotFound = errors.New("order not found")

var identRE = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

const createOrders = `
CREATE TABLE IF NOT EXISTS orders (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  item VARCHAR(255) NOT NULL,
  status VARCHAR(32) NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

// Store talks to a single HardhatDB process over the MySQL protocol.
type Store struct {
	db *sql.DB
}

// OpenStore waits until MySQL accepts a connection, creates the database, and
// creates the orders table.
func OpenStore(ctx context.Context, addr, user, password, dbname string) (*Store, error) {
	if !identRE.MatchString(dbname) {
		return nil, fmt.Errorf("invalid database name %q", dbname)
	}
	admin, err := sql.Open("mysql", mysqlDSN(addr, user, password, ""))
	if err != nil {
		return nil, err
	}
	defer admin.Close()
	if err := admin.PingContext(ctx); err != nil {
		return nil, err
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+dbname+"`"); err != nil {
		return nil, err
	}

	db, err := sql.Open("mysql", mysqlDSN(addr, user, password, dbname))
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, createOrders); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Create inserts an order with status new and reads the stored row back.
func (s *Store) Create(ctx context.Context, item string) (Order, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO orders (item, status) VALUES (?, ?)`, item, "new")
	if err != nil {
		return Order{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Order{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Get(ctx context.Context, id int64) (Order, error) {
	var order Order
	err := s.db.QueryRowContext(ctx,
		`SELECT id, item, status, created_at FROM orders WHERE id = ?`, id,
	).Scan(&order.ID, &order.Item, &order.Status, &order.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, err
	}
	return order, nil
}

// MarkDone sets status to done. A second call leaves the row done.
func (s *Store) MarkDone(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE orders SET status = ? WHERE id = ?`, "done", id)
	return err
}

func mysqlDSN(addr, user, password, dbname string) string {
	cfg := mysql.Config{
		User:                 user,
		Passwd:               password,
		Net:                  "tcp",
		Addr:                 addr,
		DBName:               dbname,
		ParseTime:            true,
		Loc:                  time.UTC,
		AllowNativePasswords: true,
		Timeout:              2 * time.Second,
	}
	return cfg.FormatDSN()
}
