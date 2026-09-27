// Package ctxerr allows storing and retrieving an error within a context.Context,
// enabling decoupled error reporting between handlers and middleware/interceptors.
package ctxerr

import (
	"context"
	"sync"
)

type errKey struct{}

type errorSlot struct {
	mu  sync.RWMutex
	err error
}

// WithSlot attaches a mutable, thread-safe error slot to the context.
func WithSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, errKey{}, &errorSlot{})
}

// SetErr assigns an error to the error slot in ctx, if present.
func SetErr(ctx context.Context, err error) {
	if s, ok := ctx.Value(errKey{}).(*errorSlot); ok {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}
}

// Err retrieves the error from the slot in ctx, or nil if none was set.
func Err(ctx context.Context) error {
	if s, ok := ctx.Value(errKey{}).(*errorSlot); ok {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.err
	}
	return nil
}

