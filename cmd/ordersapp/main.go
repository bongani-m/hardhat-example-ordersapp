// Command ordersapp stores orders in HardhatDB and caches them in HardhatKV.
// ordersapp serves HTTP and publishes an "order created" message to HardhatQ.
// ordersapp worker consumes that queue and marks the order done.
//
//	docker compose up --build
//	open http://localhost:8080
//	curl -s localhost:8080/up
//	curl -s -X POST localhost:8080/orders -H 'content-type: application/json' -d '{"item":"notebook"}'
//	curl -s localhost:8080/orders/1
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bongani-m/hardhat-example-ordersapp/internal/broker"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/cache"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/store"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/tlsconfig"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/web"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch {
	case len(os.Args) == 1:
		err = runWeb(ctx)
	case len(os.Args) == 2 && os.Args[1] == "worker":
		err = runWorker(ctx)
	default:
		err = fmt.Errorf("usage: ordersapp [worker]")
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func runWeb(ctx context.Context) error {
	st, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer st.Close()

	c, err := openCache(ctx)
	if err != nil {
		return err
	}
	defer c.Close()

	amqpURL, amqpTLS, err := amqpSettings()
	if err != nil {
		return err
	}
	log.Printf("connecting to hardhatq")
	b, err := wait(ctx, "hardhatq", func(context.Context) (*broker.Broker, error) {
		return broker.OpenBroker(amqpURL, amqpTLS)
	})
	if err != nil {
		return err
	}
	defer b.Close()

	addr := env("HTTP_ADDR", ":8080")
	log.Printf("listening on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           web.New(st, c, b),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serve(ctx, srv)
}

func runWorker(ctx context.Context) error {
	st, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer st.Close()

	c, err := openCache(ctx)
	if err != nil {
		return err
	}
	defer c.Close()

	amqpURL, amqpTLS, err := amqpSettings()
	if err != nil {
		return err
	}
	return worker.Run(ctx, amqpURL, amqpTLS, st, c)
}

func openStore(ctx context.Context) (*store.Store, error) {
	mysqlAddr := env("MYSQL_ADDR", "localhost:3306")
	mysqlCA, err := tlsconfig.LoadEnvCertPool("MYSQL_TLS_CA", "MYSQL_TLS_CA_PEM")
	if err != nil {
		return nil, err
	}
	tlsName, err := store.RegisterMySQLTLS(mysqlCA, mysqlAddr)
	if err != nil {
		return nil, err
	}
	log.Printf("connecting to hardhatdb %s", mysqlAddr)
	return wait(ctx, "hardhatdb", func(ctx context.Context) (*store.Store, error) {
		return store.OpenStore(ctx,
			mysqlAddr,
			env("MYSQL_USER", "root"),
			env("MYSQL_PASSWORD", "secret"),
			env("MYSQL_DB", "shop"),
			tlsName,
		)
	})
}

func openCache(ctx context.Context) (*cache.Cache, error) {
	kvAddr := env("KV_ADDR", "localhost:6399")
	log.Printf("connecting to hardhatkv %s", kvAddr)
	return wait(ctx, "hardhatkv", func(ctx context.Context) (*cache.Cache, error) {
		return cache.OpenCache(ctx, kvAddr, env("KV_PASSWORD", ""))
	})
}

func amqpSettings() (string, *tls.Config, error) {
	amqpURL := env("AMQP_URL", "amqp://guest:guest@localhost:5672/")
	amqpCA, err := tlsconfig.LoadEnvCertPool("AMQP_TLS_CA", "AMQP_TLS_CA_PEM")
	if err != nil {
		return "", nil, err
	}
	amqpTLS, err := tlsconfig.AMQPTLSConfig(amqpURL, amqpCA)
	if err != nil {
		return "", nil, err
	}
	return amqpURL, amqpTLS, nil
}

func serve(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func wait[T any](ctx context.Context, name string, open func(context.Context) (T, error)) (T, error) {
	var zero T
	deadline := time.Now().Add(2 * time.Minute)
	var last error
	for {
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		value, err := open(attempt)
		cancel()
		if err == nil {
			log.Printf("%s ready", name)
			return value, nil
		}
		last = err
		log.Printf("%s: %v", name, err)
		if time.Now().After(deadline) || ctx.Err() != nil {
			return zero, last
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
