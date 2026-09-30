package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blessed-go/sling/platform/httpx"
)

func TestIdempotency_PassThroughWhenNoHeader(t *testing.T) {
	store := httpx.NewMemoryStore()
	mw := httpx.Idempotency(store, 1*time.Hour)

	var calls int32
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/orders", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Fatal("unexpected Idempotent-Replayed header on non-idempotent request")
	}
}

func TestIdempotency_ReplaysCachedResponse(t *testing.T) {
	store := httpx.NewMemoryStore()
	mw := httpx.Idempotency(store, 1*time.Hour)

	var calls int32
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("X-Custom", "test-val")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"order_id":"123"}`))
	}))

	req1 := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req1.Header.Set("Idempotency-Key", "key-42")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec1.Code)
	}
	if rec1.Body.String() != `{"order_id":"123"}` {
		t.Fatalf("unexpected body: %s", rec1.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req2.Header.Set("Idempotency-Key", "key-42")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec2.Code)
	}
	if rec2.Body.String() != `{"order_id":"123"}` {
		t.Fatalf("unexpected body: %s", rec2.Body.String())
	}
	if rec2.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("expected Idempotent-Replayed: true header")
	}
	if rec2.Header().Get("X-Custom") != "test-val" {
		t.Fatal("expected preserved custom header")
	}
	if calls != 1 {
		t.Fatalf("handler must not be executed twice, calls=%d", calls)
	}
}

func TestIdempotency_InFlightConflict(t *testing.T) {
	store := httpx.NewMemoryStore()
	mw := httpx.Idempotency(store, 1*time.Hour)

	_, _ = store.TryLock(t.Context(), "idempotency:key-busy", 30*time.Second)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req.Header.Set("Idempotency-Key", "key-busy")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for in-flight request, got %d", rec.Code)
	}
}

func TestIdempotency_UnlocksOn5xx(t *testing.T) {
	store := httpx.NewMemoryStore()
	mw := httpx.Idempotency(store, 1*time.Hour)

	var calls int32
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&calls, 1)
		if c == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovered"))
	}))

	req1 := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req1.Header.Set("Idempotency-Key", "key-fail")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req2.Header.Set("Idempotency-Key", "key-fail")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 after retry, got %d", rec2.Code)
	}
	if rec2.Body.String() != "recovered" {
		t.Fatalf("unexpected body: %s", rec2.Body.String())
	}
}

func TestIdempotency_UnlocksOnPanic(t *testing.T) {
	store := httpx.NewMemoryStore()
	mw := httpx.Idempotency(store, 1*time.Hour)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom!")
	}))

	req := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req.Header.Set("Idempotency-Key", "key-panic")
	rec := httptest.NewRecorder()

	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(rec, req)
	}()

	acquired, err := store.TryLock(t.Context(), "idempotency:key-panic", time.Minute)
	if err != nil || !acquired {
		t.Fatal("lock should have been released after panic")
	}
}
