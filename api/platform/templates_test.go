package platform

import (
	"context"
	"database/sql"
	"testing"

	"github.com/atombasedev/atombase/primarystore"
	_ "github.com/mattn/go-sqlite3"
)

var testPrimaryStore *primarystore.Store

// =============================================================================
// Test Setup
// =============================================================================

// Schema for platform tables (templates, history, databases)
const platformSchema = `
CREATE TABLE atombase_schema_templates (
	id INTEGER PRIMARY KEY,
	name TEXT UNIQUE NOT NULL,
	current_version INTEGER DEFAULT 1,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE atombase_templates_history (
	id INTEGER PRIMARY KEY,
	template_id INTEGER NOT NULL REFERENCES atombase_schema_templates(id),
	version INTEGER NOT NULL,
	schema TEXT NOT NULL,
	checksum TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(template_id, version)
);

CREATE TABLE atombase_databases (
	id INTEGER PRIMARY KEY,
	name TEXT UNIQUE,
	template_id INTEGER REFERENCES atombase_schema_templates(id),
	template_version INTEGER DEFAULT 1,
	auth_token_encrypted BLOB
);

CREATE TABLE atombase_migrations (
	id INTEGER PRIMARY KEY,
	template_id INTEGER NOT NULL,
	from_version INTEGER NOT NULL,
	to_version INTEGER NOT NULL,
	sql TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	state TEXT,
	total_dbs INTEGER NOT NULL DEFAULT 0,
	completed_dbs INTEGER NOT NULL DEFAULT 0,
	failed_dbs INTEGER NOT NULL DEFAULT 0,
	started_at TEXT,
	completed_at TEXT,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if _, err := conn.Exec(platformSchema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	testPrimaryStore, err = primarystore.New(conn)
	if err != nil {
		conn.Close()
		t.Fatalf("failed to create primary store: %v", err)
	}

	api, err := NewAPI(testPrimaryStore)
	if err != nil {
		_ = testPrimaryStore.Close()
		_ = conn.Close()
		t.Fatalf("failed to initialize platform store: %v", err)
	}
	currentTestAPI = api

	return conn
}

func cleanupTestDB(t *testing.T) {
	t.Helper()
	if testPrimaryStore != nil {
		if testPrimaryStore.DB() != nil {
			_ = testPrimaryStore.DB().Close()
		}
		_ = testPrimaryStore.Close()
		testPrimaryStore = nil
	}
	currentTestAPI = nil
}

// =============================================================================
// diffSchemas Tests
// Criteria B: Many edge cases - table adds, drops, column changes
// =============================================================================

func TestDiffSchemas_AddTable(t *testing.T) {
	old := Schema{Tables: []Table{}}
	new := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{"id": {Name: "id", Type: "INTEGER"}}},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "add_table" || changes[0].Table != "users" {
		t.Errorf("got %+v, want add_table for users", changes[0])
	}
}

func TestDiffSchemas_DropTable(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{"id": {Name: "id", Type: "INTEGER"}}},
	}}
	new := Schema{Tables: []Table{}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "drop_table" || changes[0].Table != "users" {
		t.Errorf("got %+v, want drop_table for users", changes[0])
	}
}

func TestDiffSchemas_AddColumn(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	new := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"email": {Name: "email", Type: "TEXT"},
		}},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "add_column" || changes[0].Table != "users" || changes[0].Column != "email" {
		t.Errorf("got %+v, want add_column for users.email", changes[0])
	}
}

func TestDiffSchemas_DropColumn(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"email": {Name: "email", Type: "TEXT"},
		}},
	}}
	new := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "drop_column" || changes[0].Table != "users" || changes[0].Column != "email" {
		t.Errorf("got %+v, want drop_column for users.email", changes[0])
	}
}

func TestDiffSchemas_ModifyColumn_NotNull(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"email": {Name: "email", Type: "TEXT", NotNull: false},
		}},
	}}
	new := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"email": {Name: "email", Type: "TEXT", NotNull: true},
		}},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "modify_column" {
		t.Errorf("got type %s, want modify_column", changes[0].Type)
	}
}

func TestDiffSchemas_ModifyColumn_ForeignKey(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "posts", Columns: map[string]Col{
			"user_id": {Name: "user_id", Type: "INTEGER"},
		}},
	}}
	new := Schema{Tables: []Table{
		{Name: "posts", Columns: map[string]Col{
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users.id", OnDelete: "CASCADE"},
		}},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "modify_column" {
		t.Errorf("got type %s, want modify_column", changes[0].Type)
	}
}

func TestDiffSchemas_AddIndex(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{"email": {Name: "email", Type: "TEXT"}}},
	}}
	new := Schema{Tables: []Table{
		{Name: "users",
			Columns: map[string]Col{"email": {Name: "email", Type: "TEXT"}},
			Indexes: []Index{{Name: "idx_email", Columns: []string{"email"}}},
		},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "add_index" {
		t.Errorf("got type %s, want add_index", changes[0].Type)
	}
}

func TestDiffSchemas_DropIndex(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "users",
			Columns: map[string]Col{"email": {Name: "email", Type: "TEXT"}},
			Indexes: []Index{{Name: "idx_email", Columns: []string{"email"}}},
		},
	}}
	new := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{"email": {Name: "email", Type: "TEXT"}}},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "drop_index" {
		t.Errorf("got type %s, want drop_index", changes[0].Type)
	}
}

func TestDiffSchemas_AddFTS(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "posts", Columns: map[string]Col{"title": {Name: "title", Type: "TEXT"}}},
	}}
	new := Schema{Tables: []Table{
		{Name: "posts",
			Columns:    map[string]Col{"title": {Name: "title", Type: "TEXT"}},
			FTSColumns: []string{"title"},
		},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "add_fts" {
		t.Errorf("got type %s, want add_fts", changes[0].Type)
	}
}

func TestDiffSchemas_DropFTS(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "posts",
			Columns:    map[string]Col{"title": {Name: "title", Type: "TEXT"}},
			FTSColumns: []string{"title"},
		},
	}}
	new := Schema{Tables: []Table{
		{Name: "posts", Columns: map[string]Col{"title": {Name: "title", Type: "TEXT"}}},
	}}

	changes := diffSchemas(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != "drop_fts" {
		t.Errorf("got type %s, want drop_fts", changes[0].Type)
	}
}

func TestDiffSchemas_ChangePKType(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "users",
			Pk:      []string{"id"},
			Columns: map[string]Col{"id": {Name: "id", Type: "INTEGER"}},
		},
	}}
	new := Schema{Tables: []Table{
		{Name: "users",
			Pk:      []string{"id"},
			Columns: map[string]Col{"id": {Name: "id", Type: "TEXT"}},
		},
	}}

	changes := diffSchemas(old, new)

	// Should detect both modify_column and change_pk_type
	hasChangePKType := false
	for _, c := range changes {
		if c.Type == "change_pk_type" {
			hasChangePKType = true
		}
	}
	if !hasChangePKType {
		t.Errorf("expected change_pk_type in changes: %+v", changes)
	}
}

func TestDiffSchemas_NoChanges(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{"id": {Name: "id", Type: "INTEGER"}}},
	}}

	changes := diffSchemas(schema, schema)

	if len(changes) != 0 {
		t.Errorf("expected 0 changes for identical schemas, got %d", len(changes))
	}
}

func TestDiffSchemas_MultipleChanges(t *testing.T) {
	old := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT"},
		}},
		{Name: "posts", Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	new := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"email": {Name: "email", Type: "TEXT"}, // added
			// name dropped
		}},
		// posts dropped
		{Name: "comments", Columns: map[string]Col{ // added
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}

	changes := diffSchemas(old, new)

	// Should have: drop_table(posts), add_table(comments), drop_column(name), add_column(email)
	if len(changes) != 4 {
		t.Errorf("expected 4 changes, got %d: %+v", len(changes), changes)
	}
}

// =============================================================================
// columnModified Tests
// Criteria B: Many comparison edge cases
// =============================================================================

func TestColumnModified(t *testing.T) {
	tests := []struct {
		name     string
		old      Col
		new      Col
		modified bool
	}{
		{
			name:     "identical",
			old:      Col{Name: "email", Type: "TEXT"},
			new:      Col{Name: "email", Type: "TEXT"},
			modified: false,
		},
		{
			name:     "type changed",
			old:      Col{Name: "age", Type: "INTEGER"},
			new:      Col{Name: "age", Type: "TEXT"},
			modified: true,
		},
		{
			name:     "not null added",
			old:      Col{Name: "email", Type: "TEXT", NotNull: false},
			new:      Col{Name: "email", Type: "TEXT", NotNull: true},
			modified: true,
		},
		{
			name:     "unique added",
			old:      Col{Name: "email", Type: "TEXT", Unique: false},
			new:      Col{Name: "email", Type: "TEXT", Unique: true},
			modified: true,
		},
		{
			name:     "default added",
			old:      Col{Name: "status", Type: "TEXT"},
			new:      Col{Name: "status", Type: "TEXT", Default: "active"},
			modified: true,
		},
		{
			name:     "default changed",
			old:      Col{Name: "status", Type: "TEXT", Default: "active"},
			new:      Col{Name: "status", Type: "TEXT", Default: "pending"},
			modified: true,
		},
		{
			name:     "default removed",
			old:      Col{Name: "status", Type: "TEXT", Default: "active"},
			new:      Col{Name: "status", Type: "TEXT"},
			modified: true,
		},
		{
			name:     "collate changed",
			old:      Col{Name: "name", Type: "TEXT", Collate: "BINARY"},
			new:      Col{Name: "name", Type: "TEXT", Collate: "NOCASE"},
			modified: true,
		},
		{
			name:     "check added",
			old:      Col{Name: "age", Type: "INTEGER"},
			new:      Col{Name: "age", Type: "INTEGER", Check: "age >= 0"},
			modified: true,
		},
		{
			name:     "fk added",
			old:      Col{Name: "user_id", Type: "INTEGER"},
			new:      Col{Name: "user_id", Type: "INTEGER", References: "users.id"},
			modified: true,
		},
		{
			name:     "on delete changed",
			old:      Col{Name: "user_id", Type: "INTEGER", References: "users.id", OnDelete: "CASCADE"},
			new:      Col{Name: "user_id", Type: "INTEGER", References: "users.id", OnDelete: "SET NULL"},
			modified: true,
		},
		{
			name:     "generated added",
			old:      Col{Name: "full_name", Type: "TEXT"},
			new:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || ' ' || last"}},
			modified: true,
		},
		{
			name:     "generated stored changed",
			old:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || ' ' || last", Stored: false}},
			new:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || ' ' || last", Stored: true}},
			modified: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := columnModified(tt.old, tt.new)
			if result != tt.modified {
				t.Errorf("columnModified() = %v, want %v", result, tt.modified)
			}
		})
	}
}

// =============================================================================
// equalDefaults Tests
// Criteria A: Stable comparison function
// =============================================================================

func TestEqualDefaults(t *testing.T) {
	tests := []struct {
		name  string
		a     any
		b     any
		equal bool
	}{
		{"both nil", nil, nil, true},
		{"a nil", nil, "value", false},
		{"b nil", "value", nil, false},
		{"same string", "active", "active", true},
		{"different string", "active", "pending", false},
		{"same int", 42, 42, true},
		{"different int", 42, 43, false},
		{"same float", 3.14, 3.14, true},
		{"same bool", true, true, true},
		{"different bool", true, false, false},
		{"int vs float same value", 42, 42.0, true}, // JSON comparison
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := equalDefaults(tt.a, tt.b)
			if result != tt.equal {
				t.Errorf("equalDefaults(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.equal)
			}
		})
	}
}

// =============================================================================
// Template CRUD Tests
// Criteria C: Complex context - database transactions
// =============================================================================

func TestCreateTemplate(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	schema := Schema{Tables: []Table{
		{Name: "users", Pk: []string{"id"}, Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT"},
		}},
	}}

	template, err := currentTestAPI.createTemplate(context.Background(), "test_template", schema)
	if err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	if template.Name != "test_template" {
		t.Errorf("name = %s, want test_template", template.Name)
	}
	if template.CurrentVersion != 1 {
		t.Errorf("version = %d, want 1", template.CurrentVersion)
	}
	if len(template.Schema.Tables) != 1 {
		t.Errorf("tables = %d, want 1", len(template.Schema.Tables))
	}

	// Verify history entry was created
	var historyCount int
	err = conn.QueryRow("SELECT COUNT(*) FROM atombase_templates_history WHERE template_id = ?", template.ID).Scan(&historyCount)
	if err != nil {
		t.Fatalf("failed to query history: %v", err)
	}
	if historyCount != 1 {
		t.Errorf("history entries = %d, want 1", historyCount)
	}
}

func TestCreateTemplate_Duplicate(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	schema := Schema{Tables: []Table{}}

	_, err := currentTestAPI.createTemplate(context.Background(), "duplicate", schema)
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	_, err = currentTestAPI.createTemplate(context.Background(), "duplicate", schema)
	if err != ErrTemplateExists {
		t.Errorf("expected ErrTemplateExists, got %v", err)
	}
}

func TestGetTemplate(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create a template first
	schema := Schema{Tables: []Table{
		{Name: "posts", Pk: []string{"id"}, Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"title": {Name: "title", Type: "TEXT"},
		}},
	}}
	_, err := currentTestAPI.createTemplate(context.Background(), "get_test", schema)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Get it back
	template, err := currentTestAPI.getTemplate(context.Background(), "get_test")
	if err != nil {
		t.Fatalf("GetTemplate failed: %v", err)
	}

	if template.Name != "get_test" {
		t.Errorf("name = %s, want get_test", template.Name)
	}
	if len(template.Schema.Tables) != 1 {
		t.Errorf("tables = %d, want 1", len(template.Schema.Tables))
	}
	if template.Schema.Tables[0].Name != "posts" {
		t.Errorf("table name = %s, want posts", template.Schema.Tables[0].Name)
	}
}

func TestGetTemplate_NotFound(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	_, err := currentTestAPI.getTemplate(context.Background(), "nonexistent")
	if err != ErrTemplateNotFound {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestListTemplates(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create multiple templates
	schema := Schema{Tables: []Table{}}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		_, err := currentTestAPI.createTemplate(context.Background(), name, schema)
		if err != nil {
			t.Fatalf("setup failed for %s: %v", name, err)
		}
	}

	templates, err := currentTestAPI.listTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListTemplates failed: %v", err)
	}

	if len(templates) != 3 {
		t.Errorf("count = %d, want 3", len(templates))
	}

	// Should be sorted by name
	if templates[0].Name != "alpha" {
		t.Errorf("first template = %s, want alpha (sorted)", templates[0].Name)
	}
}

func TestListTemplates_Empty(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	templates, err := currentTestAPI.listTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListTemplates failed: %v", err)
	}

	if templates == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(templates) != 0 {
		t.Errorf("count = %d, want 0", len(templates))
	}
}

func TestDeleteTemplate(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create a template
	schema := Schema{Tables: []Table{}}
	created, err := currentTestAPI.createTemplate(context.Background(), "to_delete", schema)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Delete it
	err = currentTestAPI.deleteTemplate(context.Background(), "to_delete")
	if err != nil {
		t.Fatalf("DeleteTemplate failed: %v", err)
	}

	// Verify it's gone
	_, err = currentTestAPI.getTemplate(context.Background(), "to_delete")
	if err != ErrTemplateNotFound {
		t.Errorf("expected ErrTemplateNotFound after delete, got %v", err)
	}

	// Verify history was also deleted
	var historyCount int
	err = conn.QueryRow("SELECT COUNT(*) FROM atombase_templates_history WHERE template_id = ?", created.ID).Scan(&historyCount)
	if err != nil {
		t.Fatalf("failed to query history: %v", err)
	}
	if historyCount != 0 {
		t.Errorf("history entries = %d, want 0 after delete", historyCount)
	}
}

func TestDeleteTemplate_NotFound(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	err := currentTestAPI.deleteTemplate(context.Background(), "nonexistent")
	if err != ErrTemplateNotFound {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
}

func TestDeleteTemplate_InUse(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create a template
	schema := Schema{Tables: []Table{}}
	template, err := currentTestAPI.createTemplate(context.Background(), "in_use", schema)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Create a database using this template
	_, err = conn.Exec("INSERT INTO atombase_databases (name, template_id, template_version) VALUES (?, ?, 1)",
		"test_tenant", template.ID)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Try to delete - should fail
	err = currentTestAPI.deleteTemplate(context.Background(), "in_use")
	if err != ErrTemplateInUse {
		t.Errorf("expected ErrTemplateInUse, got %v", err)
	}
}

// =============================================================================
// DiffTemplate Tests
// Criteria B: Integration test for diff endpoint
// =============================================================================

func TestDiffTemplate(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create initial template
	oldSchema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT"},
		}},
	}}
	_, err := currentTestAPI.createTemplate(context.Background(), "diff_test", oldSchema)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Diff with new schema
	newSchema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"email": {Name: "email", Type: "TEXT"}, // added
			// name removed
		}},
	}}

	result, err := currentTestAPI.diffTemplate(context.Background(), "diff_test", newSchema)
	if err != nil {
		t.Fatalf("DiffTemplate failed: %v", err)
	}

	if len(result.Changes) != 2 {
		t.Errorf("expected 2 changes (add + drop column), got %d: %+v", len(result.Changes), result.Changes)
	}
}

func TestDiffTemplate_NoChanges(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	_, err := currentTestAPI.createTemplate(context.Background(), "no_change", schema)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	_, err = currentTestAPI.diffTemplate(context.Background(), "no_change", schema)
	if err != ErrNoChanges {
		t.Errorf("expected ErrNoChanges, got %v", err)
	}
}

func TestDiffTemplate_NotFound(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	_, err := currentTestAPI.diffTemplate(context.Background(), "nonexistent", Schema{})
	if err != ErrTemplateNotFound {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
}

// =============================================================================
// GetTemplateHistory Tests
// Criteria C: Multiple versions, ordering
// =============================================================================

func TestGetTemplateHistory(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create template
	schema := Schema{Tables: []Table{}}
	template, err := currentTestAPI.createTemplate(context.Background(), "history_test", schema)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Manually add more history entries for testing
	_, err = conn.Exec(`
		INSERT INTO atombase_templates_history (template_id, version, schema, checksum, created_at)
		VALUES (?, 2, '{"tables":[]}', 'checksum2', datetime('now'))
	`, template.ID)
	if err != nil {
		t.Fatalf("failed to insert history: %v", err)
	}

	history, err := currentTestAPI.getTemplateHistory(context.Background(), "history_test")
	if err != nil {
		t.Fatalf("GetTemplateHistory failed: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("expected 2 versions, got %d", len(history))
	}

	// Should be ordered by version DESC
	if history[0].Version != 2 {
		t.Errorf("first version = %d, want 2 (most recent)", history[0].Version)
	}
}

func TestGetTemplateHistory_NotFound(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	_, err := currentTestAPI.getTemplateHistory(context.Background(), "nonexistent")
	if err != ErrTemplateNotFound {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
}

// =============================================================================
// isUniqueConstraintError Tests
// Criteria A: Error detection, unlikely to change
// =============================================================================

func TestIsUniqueConstraintError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"sqlite unique", errWithMsg("UNIQUE constraint failed: users.email"), true},
		{"generic unique", errWithMsg("unique constraint violation"), true},
		{"other error", errWithMsg("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isUniqueConstraintError(tt.err)
			if result != tt.expected {
				t.Errorf("isUniqueConstraintError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// Helper to create error with message
type errWithMsg string

func (e errWithMsg) Error() string { return string(e) }
