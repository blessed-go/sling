package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockFlusherHijacker struct {
	http.ResponseWriter
	flushed bool
}

func (m *mockFlusherHijacker) Flush() {
	m.flushed = true
}

func TestStatusWriter_ImplementsFlusher(t *testing.T) {
	rec := httptest.NewRecorder()
	mock := &mockFlusherHijacker{ResponseWriter: rec}

	sw := &statusWriter{ResponseWriter: mock, status: http.StatusOK}

	flusher, ok := any(sw).(http.Flusher)
	if !ok {
		t.Fatal("statusWriter does not implement http.Flusher")
	}

	flusher.Flush()
	if !mock.flushed {
		t.Errorf("expected underlying ResponseWriter Flush() to be called")
	}

	if unwrapped := sw.Unwrap(); unwrapped != mock {
		t.Errorf("expected Unwrap() to return underlying mock ResponseWriter")
	}
}
