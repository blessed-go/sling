package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"uuid"

	"github.com/blessed-go/sling/platform/postgres"
	"github.com/blessed-go/sling/platform/redis"
)

type CachedRepo struct {
	Repository
	rdb *redis.Client
	ttl time.Duration
}

// NewCachedRepo wraps a Repository with Redis caching.
func NewCachedRepo(repo Repository, rdb *redis.Client, ttl time.Duration) *CachedRepo {
	return &CachedRepo{
		Repository: repo,
		rdb:        rdb,
		ttl:        ttl,
	}
}

func (r *CachedRepo) key(id uuid.UUID) string {
	return fmt.Sprintf("__SERVICE__:items:%s", id.String())
}

// Get implements read-through caching: checks Redis first, falls back to the underlying repository, and populates the cache.
func (r *CachedRepo) Get(ctx context.Context, id uuid.UUID) (*Item, error) {
	if postgres.HasTx(ctx) {
		return r.Repository.Get(ctx, id)
	}

	val, err := r.rdb.Get(ctx, r.key(id)).Bytes()
	if err == nil {
		var item Item
		if err := json.Unmarshal(val, &item); err == nil {
			return &item, nil
		}
	}

	item, err := r.Repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if bytes, err := json.Marshal(item); err == nil {
		_ = r.rdb.Set(ctx, r.key(id), bytes, r.ttl).Err()
	}

	return item, nil
}

// Create inserts the item into the underlying repository and populates the cache (write-through).
func (r *CachedRepo) Create(ctx context.Context, item *Item) error {
	if err := r.Repository.Create(ctx, item); err != nil {
		return err
	}

	// Cache-Aside / Write-Through: populate Redis on create, or invalidate via r.rdb.Del(ctx, r.key(item.ID)).Err()
	if bytes, err := json.Marshal(item); err == nil {
		_ = r.rdb.Set(ctx, r.key(item.ID), bytes, r.ttl).Err()
	}

	return nil
}
