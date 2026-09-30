package store

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"regexp"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/bongani-m/hardhat-example-ordersapp/internal/order"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/tlsconfig"
)

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
func OpenStore(ctx context.Context, addr, user, password, dbname, tlsName string) (*Store, error) {
	if !identRE.MatchString(dbname) {
		return nil, fmt.Errorf("invalid database name %q", dbname)
	}
	admin, err := sql.Open("mysql", mysqlDSN(addr, user, password, "", tlsName))
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

	db, err := sql.Open("mysql", mysqlDSN(addr, user, password, dbname, tlsName))
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
func (s *Store) Create(ctx context.Context, item string) (order.Order, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO orders (item, status) VALUES (?, ?)`, item, "new")
	if err != nil {
		return order.Order{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return order.Order{}, err
	}
	return s.Get(ctx, id)
}

// List returns the newest orders. limit is capped at 50.
func (s *Store) List(ctx context.Context, limit int) ([]order.Order, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, item, status, created_at FROM orders ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []order.Order{}
	for rows.Next() {
		var row order.Order
		if err := rows.Scan(&row.ID, &row.Item, &row.Status, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Get(ctx context.Context, id int64) (order.Order, error) {
	var row order.Order
	err := s.db.QueryRowContext(ctx,
		`SELECT id, item, status, created_at FROM orders WHERE id = ?`, id,
	).Scan(&row.ID, &row.Item, &row.Status, &row.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, ErrNotFound
	}
	if err != nil {
		return order.Order{}, err
	}
	return row, nil
}

// MarkDone sets status to done. A second call leaves the row done.
func (s *Store) MarkDone(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE orders SET status = ? WHERE id = ?`, "done", id)
	return err
}

func mysqlDSN(addr, user, password, dbname, tlsName string) string {
	cfg := mysql.Config{
		User:                 user,
		Passwd:               password,
		Net:                  "tcp",
		Addr:                 addr,
		DBName:               dbname,
		ParseTime:            true,
		Loc:                  time.UTC,
		AllowNativePasswords: true,
		TLSConfig:            tlsName,
		Timeout:              2 * time.Second,
	}
	return cfg.FormatDSN()
}

// RegisterMySQLTLS trusts pool and checks the server name against the host in
// addr. A nil pool leaves the connection plaintext. HardhatDB's bootstrap
// account uses caching_sha2_password, which a control-plane node accepts only
// on TLS. The certificate's names are the node's IP addresses, so the host
// must be that IP.
func RegisterMySQLTLS(pool *x509.CertPool, addr string) (string, error) {
	if pool == nil {
		return "", nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	const name = "hardhatdb"
	mysql.DeregisterTLSConfig(name)
	if err := mysql.RegisterTLSConfig(name, tlsconfig.Client(host, pool)); err != nil {
		return "", err
	}
	return name, nil
}
