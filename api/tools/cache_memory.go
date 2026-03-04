package tools

import (
	"context"
	"sync"
)

// MemoryCache is a simple in-memory cache storing bytes.
// Fallback for when Redis is not configured.
type MemoryCache struct {
	data sync.Map // string -> []byte
}

// NewMemoryCache creates a new in-memory cache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{}
}

func (c *MemoryCache) Get(ctx context.Context, key string) ([]byte, error) {
	val, ok := c.data.Load(key)
	if !ok {
		return nil, nil // Not found, no error
	}
	return val.([]byte), nil
}

func (c *MemoryCache) Set(ctx context.Context, key string, value []byte) error {
	c.data.Store(key, value)
	return nil
}

func (c *MemoryCache) Delete(ctx context.Context, key string) error {
	c.data.Delete(key)
	return nil
}

func (c *MemoryCache) Close() error {
	return nil
}
