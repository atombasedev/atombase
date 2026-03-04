package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// CachedTemplate holds schema bytes and version together.
// Schema is stored as JSON bytes to preserve type information
// when unmarshaled by the caller.
type CachedTemplate struct {
	SchemaJSON []byte `json:"schema"`
	Version    int    `json:"version"`
}

// Global cache instance
var cache Cache

// InitCache initializes the global cache instance.
func InitCache(c Cache) {
	cache = c
}

// GetCache returns the global cache instance.
func GetCache() Cache {
	return cache
}

// SetTemplate stores the current schema and version for a template.
// The schema is marshaled to JSON bytes for type-safe storage.
func SetTemplate(templateID int32, version int, schema any) {
	if cache == nil {
		return
	}

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return
	}

	key := fmt.Sprintf("template:%d", templateID)
	data, err := json.Marshal(CachedTemplate{SchemaJSON: schemaJSON, Version: version})
	if err != nil {
		return
	}
	cache.Set(context.Background(), key, data)
}

// GetTemplate retrieves the cached template (schema bytes + version).
// Returns the cached template and true if found, empty struct and false otherwise.
// Caller should unmarshal SchemaJSON to the appropriate type.
func GetTemplate(templateID int32) (CachedTemplate, bool) {
	if cache == nil {
		return CachedTemplate{}, false
	}
	key := fmt.Sprintf("template:%d", templateID)
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
	cache.Delete(context.Background(), key)
}
