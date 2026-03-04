package tools

import "context"

// Cache provides byte-oriented key-value storage.
// Optimized for Redis, with in-memory as fallback.
// Typed wrappers handle serialization (JSON).
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	Close() error
}
