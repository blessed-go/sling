package redis_test

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/blessed-go/sling/platform/app"
	"github.com/blessed-go/sling/platform/redis"
	"github.com/blessed-go/sling/platform/telemetry"
)

var (
	_ app.Pinger = (*redis.Client)(nil)
	_ io.Closer  = (*redis.Client)(nil)
)

func TestRedis_Integration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	ctx := context.Background()
	cfg := redis.Config{
		Addr:         addr,
		DialTimeout:  1 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
	}

	client, err := redis.New(ctx, cfg)
	if err != nil {
		t.Skipf("redis is not available on %s (%v), skipping integration test", addr, err)
	}
	defer client.Close()

	testKey := "sling:test:ping"
	if err := client.Set(ctx, testKey, "pong", 5*time.Second).Err(); err != nil {
		t.Fatalf("failed to execute SET: %v", err)
	}

	val, err := client.Get(ctx, testKey).Result()
	if err != nil || val != "pong" {
		t.Fatalf("expected 'pong', got %q (err: %v)", val, err)
	}

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("expected Ping to succeed on active instance: %v", err)
	}

	meter := telemetry.NewMeter("redis")
	if err := client.RegisterMetrics(meter); err != nil {
		t.Fatalf("failed to register pool metrics: %v", err)
	}
}
