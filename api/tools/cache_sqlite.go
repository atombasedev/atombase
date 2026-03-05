package tools

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteCache is a SQLite-backed cache implementation.
// Designed for LiteFS deployments on Fly.io where the SQLite file
// is replicated across nodes.
type SQLiteCache struct {
	db        *sql.DB
	keyPrefix string
	mu        sync.RWMutex
}

const sqliteCacheSchema = `
CREATE TABLE IF NOT EXISTS cache_entries (
	key TEXT PRIMARY KEY,
	value BLOB NOT NULL,
	created_at INTEGER DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_cache_created ON cache_entries(created_at);
`

const sqliteCachePragmas = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA cache_size = -10000;
PRAGMA busy_timeout = 5000;
`

// NewSQLiteCache creates a new SQLite cache.
// path is the path to the SQLite database file (e.g., "/litefs/cache.db")
// keyPrefix is prepended to all keys (e.g., "atomhost:instance:myapp:")
func NewSQLiteCache(path, keyPrefix string) (*SQLiteCache, error) {
	db, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping cache database: %w", err)
	}

	// Set pragmas for performance
	if _, err := db.Exec(sqliteCachePragmas); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set cache pragmas: %w", err)
	}

	// Initialize schema
	if _, err := db.Exec(sqliteCacheSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize cache schema: %w", err)
	}

	return &SQLiteCache{
		db:        db,
		keyPrefix: keyPrefix,
	}, nil
}

func (c *SQLiteCache) prefixedKey(key string) string {
	return c.keyPrefix + key
}

func (c *SQLiteCache) Get(ctx context.Context, key string) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var value []byte
	err := c.db.QueryRowContext(ctx,
		"SELECT value FROM cache_entries WHERE key = ?",
		c.prefixedKey(key),
	).Scan(&value)

	if err == sql.ErrNoRows {
		return nil, nil // Not found, no error
	}
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (c *SQLiteCache) Set(ctx context.Context, key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO cache_entries (key, value) VALUES (?, ?)",
		c.prefixedKey(key), value,
	)
	return err
}

func (c *SQLiteCache) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.db.ExecContext(ctx,
		"DELETE FROM cache_entries WHERE key = ?",
		c.prefixedKey(key),
	)
	return err
}

func (c *SQLiteCache) Close() error {
	return c.db.Close()
}
