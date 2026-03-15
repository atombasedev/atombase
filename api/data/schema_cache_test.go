package data

import (
	"encoding/json"
	"testing"

	"github.com/atombasedev/atombase/tools"
)

func init() {
	// Initialize cache for tests
	tools.InitCache(tools.NewMemoryCache())
}

// Test fixtures for schema cache
var (
	// Table with foreign key reference
	testTablePosts = Table{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":      {Name: "id", Type: "INTEGER", NotNull: true},
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users.id"},
			"title":   {Name: "title", Type: "TEXT", NotNull: true},
		},
	}

	// Table with multiple foreign keys
	testTableComments = Table{
		Name: "comments",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":      {Name: "id", Type: "INTEGER", NotNull: true},
			"post_id": {Name: "post_id", Type: "INTEGER", References: "posts.id"},
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users.id"},
			"body":    {Name: "body", Type: "TEXT", NotNull: true},
		},
	}

	// Table with no foreign keys
	testTableUsers = Table{
		Name: "users",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER", NotNull: true},
			"name": {Name: "name", Type: "TEXT", NotNull: false},
		},
	}
)

// TestTablesToSchemaCache_ExtractsForeignKeys verifies FK extraction from column references.
func TestTablesToSchemaCache_ExtractsForeignKeys(t *testing.T) {
	tables := []Table{testTableUsers, testTablePosts}

	cache := TablesToSchemaCache(tables)

	// Check that posts table has FK to users
	postsFks := cache.Fks["posts"]
	if len(postsFks) != 1 {
		t.Fatalf("expected 1 FK for posts, got %d", len(postsFks))
	}

	fk := postsFks[0]
	if fk.Table != "posts" {
		t.Errorf("expected FK table 'posts', got %s", fk.Table)
	}
	if fk.From != "user_id" {
		t.Errorf("expected FK from 'user_id', got %s", fk.From)
	}
	if fk.References != "users" {
		t.Errorf("expected FK references 'users', got %s", fk.References)
	}
	if fk.To != "id" {
		t.Errorf("expected FK to 'id', got %s", fk.To)
	}
}

// TestTablesToSchemaCache_MultipleForeignKeys verifies extraction of multiple FKs per table.
func TestTablesToSchemaCache_MultipleForeignKeys(t *testing.T) {
	tables := []Table{testTableUsers, testTablePosts, testTableComments}

	cache := TablesToSchemaCache(tables)

	// Comments table should have 2 FKs
	commentsFks := cache.Fks["comments"]
	if len(commentsFks) != 2 {
		t.Fatalf("expected 2 FKs for comments, got %d", len(commentsFks))
	}

	// Verify both FKs exist (order may vary)
	hasPostFK := false
	hasUserFK := false
	for _, fk := range commentsFks {
		if fk.From == "post_id" && fk.References == "posts" && fk.To == "id" {
			hasPostFK = true
		}
		if fk.From == "user_id" && fk.References == "users" && fk.To == "id" {
			hasUserFK = true
		}
	}
	if !hasPostFK {
		t.Error("missing FK for post_id -> posts.id")
	}
	if !hasUserFK {
		t.Error("missing FK for user_id -> users.id")
	}
}

// TestTablesToSchemaCache_NoForeignKeys verifies tables without FKs work correctly.
func TestTablesToSchemaCache_NoForeignKeys(t *testing.T) {
	tables := []Table{testTableUsers}

	cache := TablesToSchemaCache(tables)

	if len(cache.Fks["users"]) != 0 {
		t.Errorf("expected no FKs for users, got %d", len(cache.Fks["users"]))
	}
}

// TestTablesToSchemaCache_TablesMap verifies tables are correctly keyed by name.
func TestTablesToSchemaCache_TablesMap(t *testing.T) {
	tables := []Table{testTableUsers, testTablePosts}

	cache := TablesToSchemaCache(tables)

	if len(cache.Tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(cache.Tables))
	}
	if _, ok := cache.Tables["users"]; !ok {
		t.Error("missing 'users' table in cache")
	}
	if _, ok := cache.Tables["posts"]; !ok {
		t.Error("missing 'posts' table in cache")
	}
}

// TestSchemaCache_StoreAndRetrieve verifies the definition cache in tools package.
func TestSchemaCache_StoreAndRetrieve(t *testing.T) {
	// Store a schema with version
	schema := TablesToSchemaCache([]Table{testTableUsers})
	tools.SetDefinition(999, 1, schema)

	// Retrieve it
	cached, ok := tools.GetDefinition(999)
	if !ok {
		t.Fatal("expected to find cached definition")
	}

	if cached.Version != 1 {
		t.Errorf("expected version 1, got %d", cached.Version)
	}

	// In-memory cache stores struct directly, external cache uses SchemaJSON
	var retrieved SchemaCache
	if cached.Schema != nil {
		// Fast path: in-memory cache stores struct directly
		retrieved = cached.Schema.(SchemaCache)
	} else if len(cached.SchemaJSON) > 0 {
		// External cache: deserialize from JSON
		if err := json.Unmarshal(cached.SchemaJSON, &retrieved); err != nil {
			t.Fatalf("failed to unmarshal schema: %v", err)
		}
	} else {
		t.Fatal("expected either Schema or SchemaJSON to be populated")
	}

	if len(retrieved.Tables) != 1 {
		t.Errorf("expected 1 table, got %d", len(retrieved.Tables))
	}
	if _, ok := retrieved.Tables["users"]; !ok {
		t.Error("missing 'users' table in retrieved cache")
	}

	// Clean up
	tools.InvalidateDefinition(999)
}

// TestSchemaCache_Miss verifies cache miss behavior.
func TestSchemaCache_Miss(t *testing.T) {
	_, ok := tools.GetDefinition(99999)
	if ok {
		t.Error("expected cache miss for non-existent definition")
	}
}

// TestSchemaCache_Invalidate verifies cache invalidation.
func TestSchemaCache_Invalidate(t *testing.T) {
	schema := TablesToSchemaCache([]Table{testTableUsers})
	tools.SetDefinition(998, 1, schema)

	// Verify it's there
	_, ok := tools.GetDefinition(998)
	if !ok {
		t.Fatal("expected to find cached definition before invalidation")
	}

	// Invalidate
	tools.InvalidateDefinition(998)

	// Verify it's gone
	_, ok = tools.GetDefinition(998)
	if ok {
		t.Error("expected cache miss after invalidation")
	}
}

// TestSchemaCache_VersionUpdate verifies updating a definition's version in cache.
func TestSchemaCache_VersionUpdate(t *testing.T) {
	schema := TablesToSchemaCache([]Table{testTableUsers})

	// Store version 1
	tools.SetDefinition(997, 1, schema)

	// Verify version 1
	cached, ok := tools.GetDefinition(997)
	if !ok {
		t.Fatal("expected to find cached definition")
	}
	if cached.Version != 1 {
		t.Errorf("expected version 1, got %d", cached.Version)
	}

	// Update to version 3 (simulates a definition migration)
	tools.SetDefinition(997, 3, schema)

	// Verify version 3
	cached, ok = tools.GetDefinition(997)
	if !ok {
		t.Fatal("expected to find cached definition after update")
	}
	if cached.Version != 3 {
		t.Errorf("expected version 3 after update, got %d", cached.Version)
	}

	// Clean up
	tools.InvalidateDefinition(997)
}
