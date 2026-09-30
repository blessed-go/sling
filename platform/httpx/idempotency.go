package httpx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// IdempotencyStore defines the storage contract for idempotency locking and response caching.
type IdempotencyStore interface {
	// Get retrieves the stored response envelope for key. If key is in-flight or absent, found returns false.
	Get(ctx context.Context, key string) (data []byte, found bool, err error)
	// TryLock attempts to acquire an in-flight lock for key with a given lock TTL.
	TryLock(ctx context.Context, key string, ttl time.Duration) (acquired bool, err error)
	// Set stores the completed response envelope for key with the given retention TTL.
	Set(ctx context.Context, key string, data []byte, ttl time.Duration) error
	// Unlock releases an in-flight lock or removes the key (e.g. on 5xx failure or panic).
	Unlock(ctx context.Context, key string) error
}

type responseEnvelope struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

type IdempotencyConfig struct {
	HeaderName  string        // Header to read (default: "Idempotency-Key")
	LockTTL     time.Duration // In-flight lock TTL (default: 30s)
	ResponseTTL time.Duration // Response retention TTL (default: 24h)
	KeyPrefix   string        // Prefix for storage keys (default: "idempotency:")
}

type IdempotencyOption func(*IdempotencyConfig)

func WithHeaderName(name string) IdempotencyOption {
	return func(c *IdempotencyConfig) { c.HeaderName = name }
}

func WithLockTTL(d time.Duration) IdempotencyOption {
	return func(c *IdempotencyConfig) { c.LockTTL = d }
}

func WithKeyPrefix(prefix string) IdempotencyOption {
	return func(c *IdempotencyConfig) { c.KeyPrefix = prefix }
}

func Idempotency(store IdempotencyStore, responseTTL time.Duration, opts ...IdempotencyOption) func(http.Handler) http.Handler {
	cfg := IdempotencyConfig{
		HeaderName:  "Idempotency-Key",
		LockTTL:     30 * time.Second,
		ResponseTTL: responseTTL,
		KeyPrefix:   "idempotency:",
	}
	if cfg.ResponseTTL <= 0 {
		cfg.ResponseTTL = 24 * time.Hour
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := r.Header.Get(cfg.HeaderName)
			if rawKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			storageKey := cfg.KeyPrefix + rawKey

			data, found, err := store.Get(r.Context(), storageKey)
			if err != nil {
				WriteError(w, r, Error{Status: http.StatusInternalServerError, Msg: "failed to check idempotency store"})
				return
			}
			if found {
				var env responseEnvelope
				if err := json.Unmarshal(data, &env); err == nil {
					for k, vv := range env.Headers {
						for _, v := range vv {
							w.Header().Add(k, v)
						}
					}
					w.Header().Set("Idempotent-Replayed", "true")
					w.WriteHeader(env.Status)
					_, _ = w.Write(env.Body)
					return
				}
			}

			acquired, err := store.TryLock(r.Context(), storageKey, cfg.LockTTL)
			if err != nil {
				WriteError(w, r, Error{Status: http.StatusInternalServerError, Msg: "failed to acquire idempotency lock"})
				return
			}
			if !acquired {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "request in progress"})
				return
			}

			rec := &bufferedResponseWriter{ResponseWriter: w, status: http.StatusOK}
			panicked := true

			defer func() {
				if panicked {
					unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					_ = store.Unlock(unlockCtx, storageKey)
				}
			}()

			next.ServeHTTP(rec, r)
			panicked = false

			if rec.status >= http.StatusInternalServerError {
				_ = store.Unlock(r.Context(), storageKey)
				return
			}

			env := responseEnvelope{
				Status:  rec.status,
				Headers: rec.Header().Clone(),
				Body:    rec.buf.Bytes(),
			}
			payload, err := json.Marshal(env)
			if err == nil {
				_ = store.Set(r.Context(), storageKey, payload, cfg.ResponseTTL)
			}
		})
	}
}

type bufferedResponseWriter struct {
	http.ResponseWriter
	status      int
	buf         bytes.Buffer
	wroteHeader bool
}

func (b *bufferedResponseWriter) WriteHeader(status int) {
	if b.wroteHeader {
		return
	}
	b.status = status
	b.wroteHeader = true
	b.ResponseWriter.WriteHeader(status)
}

func (b *bufferedResponseWriter) Write(p []byte) (int, error) {
	if !b.wroteHeader {
		b.WriteHeader(http.StatusOK)
	}
	b.buf.Write(p)
	return b.ResponseWriter.Write(p)
}

func (b *bufferedResponseWriter) Unwrap() http.ResponseWriter {
	return b.ResponseWriter
}

func (b *bufferedResponseWriter) Flush() {
	if f, ok := b.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (b *bufferedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := b.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

type memoryItem struct {
	data      []byte
	expiresAt time.Time
	inFlight  bool
}

type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]memoryItem
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items: make(map[string]memoryItem),
	}
}

func (m *MemoryStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	item, ok := m.items[key]
	if !ok || item.inFlight || time.Now().After(item.expiresAt) {
		return nil, false, nil
	}
	return item.data, true, nil
}

func (m *MemoryStore) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if item, ok := m.items[key]; ok {
		if now.Before(item.expiresAt) {
			return false, nil
		}
	}

	m.items[key] = memoryItem{
		inFlight:  true,
		expiresAt: now.Add(ttl),
	}
	return true, nil
}

func (m *MemoryStore) Set(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.items[key] = memoryItem{
		data:      data,
		inFlight:  false,
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (m *MemoryStore) Unlock(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.items, key)
	return nil
}
