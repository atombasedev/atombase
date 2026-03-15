package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// CachedDefinition holds parsed schema and version.
// For in-memory cache, the schema is stored as the actual Go struct.
// For external caches, it's serialized to JSON.
type CachedDefinition struct {
	Schema  any `json:"-"` // Parsed schema struct (in-memory only)
	Version int `json:"version"`

	// For external cache serialization
	SchemaJSON []byte `json:"schema,omitempty"`
}

// CachedDatabase holds database metadata.
type CachedDatabase struct {
	ID              string `json:"id"`
	DefinitionID    int32  `json:"definition_id"`
	DatabaseVersion int    `json:"version"`
	AuthToken       string `json:"-"` // Decrypted token, in-memory only (not serialized to external cache)
}

// Global cache instance
var cache Cache

// memCache is the direct reference when using MemoryCache (avoids type assertion per call)
var memCache *MemoryCache

// InitCache initializes the global cache instance.
func InitCache(c Cache) {
	cache = c
	// Keep direct reference to MemoryCache for fast path
	if mc, ok := c.(*MemoryCache); ok {
		memCache = mc
	} else {
		memCache = nil
	}
}

// GetCache returns the global cache instance.
func GetCache() Cache {
	return cache
}

// SetDefinition stores the schema and version for a definition.
// Uses direct struct storage for in-memory cache (no serialization).
func SetDefinition(definitionID int32, version int, schema any) {
	if cache == nil {
		return
	}

	key := fmt.Sprintf("definition:%d", definitionID)

	// Fast path: in-memory cache stores struct directly
	if memCache != nil {
		memCache.SetValue(key, &CachedDefinition{Schema: schema, Version: version})
		return
	}

	// External cache: serialize to JSON
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return
	}
	data, err := json.Marshal(CachedDefinition{SchemaJSON: schemaJSON, Version: version})
	if err != nil {
		return
	}
	cache.Set(context.Background(), key, data)
}

// GetDefinition retrieves the cached definition.
// Returns the cached definition and true if found.
func GetDefinition(definitionID int32) (CachedDefinition, bool) {
	if cache == nil {
		return CachedDefinition{}, false
	}

	key := fmt.Sprintf("definition:%d", definitionID)

	// Fast path: in-memory cache returns struct directly
	if memCache != nil {
		if val := memCache.GetValue(key); val != nil {
			return *val.(*CachedDefinition), true
		}
		return CachedDefinition{}, false
	}

	// External cache: deserialize from JSON
	data, err := cache.Get(context.Background(), key)
	if err != nil || data == nil {
		return CachedDefinition{}, false
	}
	var cached CachedDefinition
	if err := json.Unmarshal(data, &cached); err != nil {
		return CachedDefinition{}, false
	}
	return cached, true
}

// InvalidateDefinition removes a definition from cache.
func InvalidateDefinition(definitionID int32) {
	if cache == nil {
		return
	}
	key := fmt.Sprintf("definition:%d", definitionID)

	if memCache != nil {
		memCache.DeleteValue(key)
	}
	cache.Delete(context.Background(), key)
}

// SetDatabase stores database metadata in cache.
func SetDatabase(name string, meta CachedDatabase) {
	if cache == nil {
		return
	}

	key := fmt.Sprintf("db:%s", name)

	// Fast path: in-memory cache stores struct directly
	if memCache != nil {
		memCache.SetValue(key, &meta)
		return
	}

	// External cache: serialize to JSON
	data, err := json.Marshal(meta)
	if err != nil {
		return
	}
	cache.Set(context.Background(), key, data)
}

// GetDatabase retrieves cached database metadata.
func GetDatabase(name string) (CachedDatabase, bool) {
	if cache == nil {
		return CachedDatabase{}, false
	}

	key := fmt.Sprintf("db:%s", name)

	// Fast path: in-memory cache returns struct directly
	if memCache != nil {
		if val := memCache.GetValue(key); val != nil {
			return *val.(*CachedDatabase), true
		}
		return CachedDatabase{}, false
	}

	// External cache: deserialize from JSON
	data, err := cache.Get(context.Background(), key)
	if err != nil || data == nil {
		return CachedDatabase{}, false
	}
	var meta CachedDatabase
	if err := json.Unmarshal(data, &meta); err != nil {
		return CachedDatabase{}, false
	}
	return meta, true
}

// InvalidateDatabase removes database metadata from cache.
func InvalidateDatabase(name string) {
	if cache == nil {
		return
	}
	key := fmt.Sprintf("db:%s", name)

	if memCache != nil {
		memCache.DeleteValue(key)
	}
	cache.Delete(context.Background(), key)
}

// UpdateDatabaseVersion updates just the version in cached database metadata.
func UpdateDatabaseVersion(name string, newVersion int) {
	meta, ok := GetDatabase(name)
	if !ok {
		return
	}
	meta.DatabaseVersion = newVersion
	SetDatabase(name, meta)
}
