package tools

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache is a Redis-backed cache implementation.
type RedisCache struct {
	client    *redis.Client
	keyPrefix string
}

// NewRedisCache creates a new Redis cache.
// url is the Redis connection URL (e.g., "redis://localhost:6379")
// password is the auth password (can be empty)
// keyPrefix is prepended to all keys (e.g., "atomhost:instance:myapp:")
func NewRedisCache(url, password, keyPrefix string) (*RedisCache, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	if password != "" {
		opts.Password = password
	}

	client := redis.NewClient(opts)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &RedisCache{
		client:    client,
		keyPrefix: keyPrefix,
	}, nil
}

func (c *RedisCache) prefixedKey(key string) string {
	return c.keyPrefix + key
}

func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := c.client.Get(ctx, c.prefixedKey(key)).Bytes()
	if err == redis.Nil {
		return nil, nil // Not found, no error
	}
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, value []byte) error {
	// No expiration - cache entries persist until invalidated
	return c.client.Set(ctx, c.prefixedKey(key), value, 0).Err()
}

func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.prefixedKey(key)).Err()
}

func (c *RedisCache) Close() error {
	return c.client.Close()
}
