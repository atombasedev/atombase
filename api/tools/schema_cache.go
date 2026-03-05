package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// CachedTemplate holds parsed schema and version.
// For in-memory cache, the schema is stored as the actual Go struct.
// For external caches, it's serialized to JSON.
type CachedTemplate struct {
	Schema  any `json:"-"`      // Parsed schema struct (in-memory only)
	Version int `json:"version"`

	// For external cache serialization
	SchemaJSON []byte `json:"schema,omitempty"`
}

// CachedDatabase holds database metadata.
type CachedDatabase struct {
	ID              int32 `json:"id"`
	TemplateID      int32 `json:"template_id"`
	DatabaseVersion int   `json:"version"`
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

// SetTemplate stores the schema and version for a template.
// Uses direct struct storage for in-memory cache (no serialization).
func SetTemplate(templateID int32, version int, schema any) {
	if cache == nil {
		return
	}

	key := fmt.Sprintf("template:%d", templateID)

	// Fast path: in-memory cache stores struct directly
	if memCache != nil {
		memCache.SetValue(key, &CachedTemplate{Schema: schema, Version: version})
		return
	}

	// External cache: serialize to JSON
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return
	}
	data, err := json.Marshal(CachedTemplate{SchemaJSON: schemaJSON, Version: version})
	if err != nil {
		return
	}
	cache.Set(context.Background(), key, data)
}

// GetTemplate retrieves the cached template.
// Returns the cached template and true if found.
func GetTemplate(templateID int32) (CachedTemplate, bool) {
	if cache == nil {
		return CachedTemplate{}, false
	}

	key := fmt.Sprintf("template:%d", templateID)

	// Fast path: in-memory cache returns struct directly
	if memCache != nil {
		if val := memCache.GetValue(key); val != nil {
			return *val.(*CachedTemplate), true
		}
		return CachedTemplate{}, false
	}

	// External cache: deserialize from JSON
	data, err := cache.Get(context.Background(), key)
	if err != nil || data == nil {
		return CachedTemplate{}, false
	}
	var cached CachedTemplate
	if err := json.Unmarshal(data, &cached); err != nil {
		return CachedTemplate{}, false
	}
	return cached, true
}

// InvalidateTemplate removes a template from cache.
func InvalidateTemplate(templateID int32) {
	if cache == nil {
		return
	}
	key := fmt.Sprintf("template:%d", templateID)

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
