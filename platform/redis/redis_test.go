package redis_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/blessed-go/sling/platform/redis"
	"github.com/blessed-go/sling/platform/telemetry"
)

func TestRedisIntegration(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker is not available, skipping integration test")
	}

	t.Log("starting temporary redis container...")
	containerID := startRedisContainer(t)
	defer stopContainer(containerID)

	hostPort := getContainerPort(t, containerID, "6379")
	t.Logf("redis container ready on 127.0.0.1:%s", hostPort)

	ctx := context.Background()
	cfg := redis.Config{
		Addr:         "127.0.0.1:" + hostPort,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}

	client, err := redis.New(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to redis: %v", err)
	}
	defer client.Close()

	t.Log("testing set/get commands...")
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
	t.Log("set/get verified successfully")

	t.Log("verifying telemetry metrics registration...")
	meter := telemetry.NewMeter("redis")
	if err := client.RegisterMetrics(meter); err != nil {
		t.Fatalf("failed to register redis metrics: %v", err)
	}

	t.Log("testing ping readiness contract...")
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("expected Ping to succeed on living redis, got: %v", err)
	}

	t.Log("stopping redis container to test probe failure...")
	_ = exec.Command("docker", "stop", containerID).Run()

	pingCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err == nil {
		t.Fatal("expected Ping to fail after container stopped, but it succeeded")
	}
	t.Log("ping successfully detected connection loss")
}

func startRedisContainer(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("docker", "run", "-d", "-P", "--rm", "valkey/valkey:9")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to start redis container: %v\n%s", err, string(out))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func getContainerPort(t *testing.T, containerID, internalPort string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "port", containerID, internalPort).CombinedOutput()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) > 0 {
				parts := strings.Split(lines[0], ":")
				if len(parts) >= 2 {
					return parts[len(parts)-1]
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("failed to get mapped port for container %s", containerID)
	return ""
}

func stopContainer(containerID string) {
	_ = exec.Command("docker", "rm", "-f", containerID).Run()
}
