package ctxerr_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/blessed-go/sling/platform/ctxerr"
)

func TestCtxErr_Basic(t *testing.T) {
	ctx := context.Background()
	if err := ctxerr.Err(ctx); err != nil {
		t.Fatalf("expected nil error on empty context, got %v", err)
	}

	ctx = ctxerr.WithSlot(ctx)
	if err := ctxerr.Err(ctx); err != nil {
		t.Fatalf("expected nil error on newly initialized slot, got %v", err)
	}

	testErr := errors.New("something went wrong")
	ctxerr.SetErr(ctx, testErr)

	if err := ctxerr.Err(ctx); !errors.Is(err, testErr) {
		t.Fatalf("expected error %v, got %v", testErr, err)
	}
}

func TestCtxErr_Concurrent(t *testing.T) {
	ctx := ctxerr.WithSlot(context.Background())

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	for i := 0; i < numGoroutines; i++ {
		workerID := i
		go func() {
			defer wg.Done()
			ctxerr.SetErr(ctx, fmt.Errorf("worker error %d", workerID))
		}()

		go func() {
			defer wg.Done()
			_ = ctxerr.Err(ctx)
		}()
	}

	wg.Wait()
}
