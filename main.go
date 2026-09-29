// Command ordersapp stores orders in HardhatDB, caches them in HardhatKV, and
// marks them done from a HardhatQ queue.
//
//	docker compose up --build
//	curl -s localhost:8080/health
//	curl -s -X POST localhost:8080/orders -H 'content-type: application/json' -d '{"item":"notebook"}'
//	curl -s localhost:8080/orders/1
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("connecting to hardhatdb %s", env("MYSQL_ADDR", "localhost:3306"))
	store, err := wait(ctx, "hardhatdb", func(ctx context.Context) (*Store, error) {
		return OpenStore(ctx,
			env("MYSQL_ADDR", "localhost:3306"),
			env("MYSQL_USER", "root"),
			env("MYSQL_PASSWORD", "secret"),
			env("MYSQL_DB", "shop"),
		)
	})
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	log.Printf("connecting to hardhatkv %s", env("KV_ADDR", "localhost:6399"))
	cache, err := wait(ctx, "hardhatkv", func(ctx context.Context) (*Cache, error) {
		return OpenCache(ctx, env("KV_ADDR", "localhost:6399"))
	})
	if err != nil {
		log.Fatal(err)
	}
	defer cache.Close()

	amqpURL := env("AMQP_URL", "amqp://guest:guest@localhost:5672/")
	log.Printf("connecting to hardhatq")
	broker, err := wait(ctx, "hardhatq", func(context.Context) (*Broker, error) {
		return OpenBroker(amqpURL)
	})
	if err != nil {
		log.Fatal(err)
	}
	defer broker.Close()

	if err := broker.Consume(ctx, func(ctx context.Context, order Order) error {
		if err := store.MarkDone(ctx, order.ID); err != nil {
			return err
		}
		updated, err := store.Get(ctx, order.ID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return cache.Set(ctx, updated)
	}); err != nil {
		log.Fatal(err)
	}

	app := &server{store: store, cache: cache, broker: broker}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", app.health)
	mux.HandleFunc("POST /orders", app.createOrder)
	mux.HandleFunc("GET /orders/{id}", app.getOrder)

	addr := env("HTTP_ADDR", ":8080")
	log.Printf("listening on %s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

type server struct {
	store  *Store
	cache  *Cache
	broker *Broker
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	body := map[string]string{}
	code := http.StatusOK
	check := func(name string, err error) {
		if err != nil {
			body[name] = err.Error()
			code = http.StatusServiceUnavailable
			return
		}
		body[name] = "ok"
	}
	check("hardhatdb", s.store.Ping(ctx))
	check("hardhatkv", s.cache.Ping(ctx))
	check("hardhatq", s.broker.Ping(ctx))
	writeJSON(w, code, body)
}

func (s *server) createOrder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Item string `json:"item"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	item := strings.TrimSpace(req.Item)
	if item == "" || len(item) > 255 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "item is required"})
		return
	}

	order, err := s.store.Create(r.Context(), item)
	if err != nil {
		log.Printf("create order: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	if err := s.cache.Set(r.Context(), order); err != nil {
		log.Printf("cache order %d: %v", order.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cache failed"})
		return
	}
	if err := s.broker.Publish(r.Context(), order); err != nil {
		log.Printf("publish order %d: %v", order.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "publish failed"})
		return
	}
	writeJSON(w, http.StatusCreated, order)
}

func (s *server) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	order, ok, err := s.cache.Get(r.Context(), id)
	if err != nil {
		log.Printf("cache get %d: %v", id, err)
		ok = false
	}
	if !ok {
		order, err = s.store.Get(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found"})
			return
		}
		if err != nil {
			log.Printf("get order %d: %v", id, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get failed"})
			return
		}
		if err := s.cache.Set(r.Context(), order); err != nil {
			log.Printf("cache fill %d: %v", id, err)
		}
	}
	writeJSON(w, http.StatusOK, order)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
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
