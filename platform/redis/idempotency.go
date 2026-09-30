package redis

import (
	"context"
	"errors"
	"time"

	"github.com/blessed-go/sling/platform/httpx"
	"github.com/redis/go-redis/v9"
)

const inFlightSentinel = "__SLING_IN_FLIGHT__"

var _ httpx.IdempotencyStore = (*IdempotencyStore)(nil)

// IdempotencyStore implements httpx.IdempotencyStore using Redis / Valkey.
type IdempotencyStore struct {
	client *Client
}

// IdempotencyStore returns a storage adapter backed by this Redis client.
func (c *Client) IdempotencyStore() *IdempotencyStore {
	return &IdempotencyStore{client: c}
}

// Get retrieves the stored response blob. Returns found=false if the key is absent or in-flight.
func (s *IdempotencyStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, err
	}

	if string(val) == inFlightSentinel {
		return nil, false, nil
	}

	return val, true, nil
}

// TryLock atomically acquires an in-flight lock using SET NX.
func (s *IdempotencyStore) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := s.client.SetNX(ctx, key, inFlightSentinel, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Set stores the completed response payload, overwriting the in-flight lock and setting full TTL.
func (s *IdempotencyStore) Set(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	return s.client.Set(ctx, key, data, ttl).Err()
}

// Unlock deletes the key, clearing in-flight locks or releasing on error.
func (s *IdempotencyStore) Unlock(ctx context.Context, key string) error {
	return s.client.Del(ctx, key).Err()
}
