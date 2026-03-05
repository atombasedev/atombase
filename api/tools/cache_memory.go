package tools

import (
	"context"
	"sync"
)

// MemoryCache is an in-memory cache optimized for local access.
// Stores Go values directly (no serialization) for hot path performance.
// Also implements the byte-based Cache interface for compatibility.
type MemoryCache struct {
	bytes  sync.Map // string -> []byte (for Cache interface compatibility)
	values sync.Map // string -> any (for direct struct storage, no serialization)
}

// NewMemoryCache creates a new in-memory cache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{}
}

// Get retrieves bytes from cache (Cache interface).
func (c *MemoryCache) Get(ctx context.Context, key string) ([]byte, error) {
	val, ok := c.bytes.Load(key)
	if !ok {
		return nil, nil
	}
	return val.([]byte), nil
}

// Set stores bytes in cache (Cache interface).
func (c *MemoryCache) Set(ctx context.Context, key string, value []byte) error {
	c.bytes.Store(key, value)
	return nil
}

// Delete removes an entry from cache (Cache interface).
func (c *MemoryCache) Delete(ctx context.Context, key string) error {
	c.bytes.Delete(key)
	c.values.Delete(key)
	return nil
}

// Close is a no-op for memory cache.
func (c *MemoryCache) Close() error {
	return nil
}

// GetValue retrieves a Go value directly (no deserialization).
// Returns nil if not found.
func (c *MemoryCache) GetValue(key string) any {
	val, ok := c.values.Load(key)
	if !ok {
		return nil
	}
	return val
}

// SetValue stores a Go value directly (no serialization).
func (c *MemoryCache) SetValue(key string, value any) {
	c.values.Store(key, value)
}

// DeleteValue removes a value from the direct cache.
func (c *MemoryCache) DeleteValue(key string) {
	c.values.Delete(key)
}
