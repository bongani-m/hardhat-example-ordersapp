package web

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bongani-m/hardhat-example-ordersapp/internal/broker"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/cache"
	"github.com/bongani-m/hardhat-example-ordersapp/internal/store"
)

// New is the orders HTTP API. It writes HardhatDB, caches the row, and
// publishes to HardhatQ. It does not consume the queue.
func New(st *store.Store, c *cache.Cache, b *broker.Broker) http.Handler {
	s := &server{store: st, cache: c, broker: b}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /up", s.health)
	mux.HandleFunc("POST /orders", s.createOrder)
	mux.HandleFunc("GET /orders/{id}", s.getOrder)
	return mux
}

type server struct {
	store  *store.Store
	cache  *cache.Cache
	broker *broker.Broker
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

	row, err := s.store.Create(r.Context(), item)
	if err != nil {
		log.Printf("create order: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	if err := s.cache.Set(r.Context(), row); err != nil {
		log.Printf("cache order %d: %v", row.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cache failed"})
		return
	}
	if err := s.broker.Publish(r.Context(), row); err != nil {
		log.Printf("publish order %d: %v", row.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "publish failed"})
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (s *server) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	row, ok, err := s.cache.Get(r.Context(), id)
	if err != nil {
		log.Printf("cache get %d: %v", id, err)
		ok = false
	}
	if !ok {
		row, err = s.store.Get(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found"})
			return
		}
		if err != nil {
			log.Printf("get order %d: %v", id, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get failed"})
			return
		}
		if err := s.cache.Set(r.Context(), row); err != nil {
			log.Printf("cache fill %d: %v", id, err)
		}
	}
	writeJSON(w, http.StatusOK, row)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
