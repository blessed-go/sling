// Package run provides primitives for running concurrent components as a unified group.
package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// Func defines a runnable component function.
type Func func(ctx context.Context) error

type component struct {
	name string
	fn   Func
}

// Group coordinates the execution of multiple concurrent components.
type Group struct {
	components []component
}

// Add registers a named component to the group.
func (g *Group) Add(name string, fn Func) {
	g.components = append(g.components, component{
		name: name,
		fn:   fn,
	})
}

// Run executes all components concurrently. If any component returns,
// the context is canceled for all remaining components and Run waits for all to exit.
func (g *Group) Run(ctx context.Context) error {
	if len(g.components) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		name string
		err  error
	}

	resChan := make(chan result, len(g.components))
	var wg sync.WaitGroup

	for _, c := range g.components {
		wg.Add(1)
		go func(comp component) {
			defer wg.Done()

			err := comp.fn(ctx)

			if ctx.Err() == nil {
				if err != nil {
					slog.Error("component crashed unexpectedly",
						"component", comp.name,
						"err", err,
					)
				} else {
					slog.Warn("component exited prematurely",
						"component", comp.name,
					)
				}
			}

			resChan <- result{name: comp.name, err: err}
			cancel()
		}(c)
	}

	first := <-resChan

	wg.Wait()
	close(resChan)

	var allErrs []error
	if first.err != nil && !errors.Is(first.err, context.Canceled) {
		allErrs = append(allErrs, fmt.Errorf("component %q: %w", first.name, first.err))
	}

	for r := range resChan {
		if r.err != nil && !errors.Is(r.err, context.Canceled) {
			allErrs = append(allErrs, fmt.Errorf("component %q: %w", r.name, r.err))
		}
	}

	_ = os.Stdout.Sync()
	_ = os.Stderr.Sync()

	return errors.Join(allErrs...)
}
