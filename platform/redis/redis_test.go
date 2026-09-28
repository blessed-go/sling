package redis_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/blessed-go/sling/platform/redis"
	"github.com/blessed-go/sling/platform/telemetry"
)

func TestRedisIntegration(t *testing.T) {
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
		t.Skipf("Valkey/Redis is not running on %s (%v), skipping integration test", addr, err)
	}
	defer client.Close()

	testKey := "sling:test:key"
	testVal := "super_secret_payload"

	if err := client.Set(ctx, testKey, testVal, 10*time.Second).Err(); err != nil {
		t.Fatalf("failed to SET key: %v", err)
	}

	gotVal, err := client.Get(ctx, testKey).Result()
	if err != nil {
		t.Fatalf("failed to GET key: %v", err)
	}
	if gotVal != testVal {
		t.Fatalf("expected value %q, got %q", testVal, gotVal)
	}

	meter := telemetry.NewMeter("redis")
	if err := client.RegisterMetrics(meter); err != nil {
		t.Fatalf("failed to register redis metrics: %v", err)
	}

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("expected Ping to succeed, got: %v", err)
	}

	closedPort := getUnusedPort(t)
	brokenCfg := redis.Config{
		Addr:        fmt.Sprintf("127.0.0.1:%d", closedPort),
		DialTimeout: 50 * time.Millisecond,
	}
	brokenClient, _ := redis.New(ctx, brokenCfg) // Создаем клиент без New(fail-fast), чтобы проверить Ping
	defer brokenClient.Close()

	pingCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := brokenClient.Ping(pingCtx); err == nil {
		t.Fatal("expected Ping to fail on unreachable address, but it succeeded")
	}
}

func getUnusedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}
