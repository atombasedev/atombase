package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// Test Schemas
// =============================================================================

// Basic table for WHERE clause testing
const schemaUsers = `
CREATE TABLE users (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	email TEXT UNIQUE,
	age INTEGER,
	status TEXT DEFAULT 'active'
);
`

// Composite primary key (2-column) - explicitly mentioned in testing philosophy
const schemaUserRoles = `
CREATE TABLE user_roles (
	user_id INTEGER NOT NULL,
	role_id INTEGER NOT NULL,
	granted_at TEXT,
	PRIMARY KEY (user_id, role_id)
);
`

// Triple composite key - edge case
const schemaAuditLog = `
CREATE TABLE audit_log (
	tenant_id INTEGER NOT NULL,
	entity_type TEXT NOT NULL,
	entity_id INTEGER NOT NULL,
	action TEXT,
	PRIMARY KEY (tenant_id, entity_type, entity_id)
);
`

// =============================================================================
// Helper Functions
// =============================================================================

func setupTestDB(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	return db
}

func loadSchema(t *testing.T, db *sql.DB) SchemaCache {
	t.Helper()
	tables, err := SchemaCols(db)
	if err != nil {
		t.Fatalf("failed to load schema: %v", err)
	}
	fks, err := schemaFks(db)
	if err != nil {
		t.Fatalf("failed to load fks: %v", err)
	}
	ftsTables, err := schemaFTS(db)
	if err != nil {
		t.Fatalf("failed to load fts: %v", err)
	}
	return SchemaCache{Tables: tables, Fks: fks, FTSTables: ftsTables}
}

// =============================================================================
// BuildWhereFromJSON Tests
// Criteria B: 12+ operators, NOT variants, AND/OR combinations
// =============================================================================

func TestBuildWhereFromJSON_Operators(t *testing.T) {
	db := setupTestDB(t, schemaUsers)
	defer db.Close()
	schema := loadSchema(t, db)
	table := schema.Tables["users"]

	tests := []struct {
		name     string
		where    []map[string]any
		wantSQL  string // substring to check for
		wantArgs int    // number of args expected
		wantErr  bool
	}{
		// Basic operators
		{"eq", []map[string]any{{"id": map[string]any{"eq": 5}}}, "= ?", 1, false},
		{"neq", []map[string]any{{"id": map[string]any{"neq": 5}}}, "!= ?", 1, false},
		{"gt", []map[string]any{{"age": map[string]any{"gt": 18}}}, "> ?", 1, false},
		{"gte", []map[string]any{{"age": map[string]any{"gte": 18}}}, ">= ?", 1, false},
		{"lt", []map[string]any{{"age": map[string]any{"lt": 65}}}, "< ?", 1, false},
		{"lte", []map[string]any{{"age": map[string]any{"lte": 65}}}, "<= ?", 1, false},
		{"like", []map[string]any{{"name": map[string]any{"like": "%smith%"}}}, "LIKE ?", 1, false},
		{"glob", []map[string]any{{"name": map[string]any{"glob": "*smith*"}}}, "GLOB ?", 1, false},

		// IS NULL
		{"is null", []map[string]any{{"email": map[string]any{"is": nil}}}, "IS NULL", 0, false},

		// IN array
		{"in", []map[string]any{{"status": map[string]any{"in": []any{"active", "pending"}}}}, "IN (?, ?)", 2, false},
		{"in empty error", []map[string]any{{"status": map[string]any{"in": []any{}}}}, "", 0, true},

		// BETWEEN
		{"between", []map[string]any{{"age": map[string]any{"between": []any{18, 65}}}}, "BETWEEN ? AND ?", 2, false},
		{"between wrong count", []map[string]any{{"age": map[string]any{"between": []any{18}}}}, "", 0, true},

		// NOT variants
		{"not eq", []map[string]any{{"id": map[string]any{"not": map[string]any{"eq": 5}}}}, "!= ?", 1, false},
		{"not in", []map[string]any{{"status": map[string]any{"not": map[string]any{"in": []any{"banned"}}}}}, "NOT IN", 1, false},
		{"not is null", []map[string]any{{"email": map[string]any{"not": map[string]any{"is": nil}}}}, "IS NOT NULL", 0, false},
		{"not like", []map[string]any{{"name": map[string]any{"not": map[string]any{"like": "%test%"}}}}, "NOT LIKE", 1, false},

		// Invalid operator
		{"invalid op", []map[string]any{{"id": map[string]any{"invalid": 5}}}, "", 0, true},

		// Invalid column
		{"invalid column", []map[string]any{{"nonexistent": map[string]any{"eq": 5}}}, "", 0, true},

		// Empty where
		{"empty", []map[string]any{}, "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args, err := table.BuildWhereFromJSON(tt.where, schema)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.wantSQL != "" && !strings.Contains(sql, tt.wantSQL) {
				t.Errorf("SQL = %q, want substring %q", sql, tt.wantSQL)
			}

			if len(args) != tt.wantArgs {
				t.Errorf("got %d args, want %d", len(args), tt.wantArgs)
			}
		})
	}
}

func TestBuildWhereFromJSON_OrConditions(t *testing.T) {
	db := setupTestDB(t, schemaUsers)
	defer db.Close()
	schema := loadSchema(t, db)
	table := schema.Tables["users"]

	// OR with multiple conditions
	where := []map[string]any{
		{"or": []any{
			map[string]any{"status": map[string]any{"eq": "active"}},
			map[string]any{"status": map[string]any{"eq": "pending"}},
		}},
	}

	sql, args, err := table.BuildWhereFromJSON(where, schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(sql, "OR") {
		t.Errorf("SQL should contain OR: %s", sql)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
}

func TestBuildWhereFromJSON_ColumnReference(t *testing.T) {
	db := setupTestDB(t, `
		CREATE TABLE posts (
			id INTEGER PRIMARY KEY,
			created_at TEXT,
			updated_at TEXT
		);
	`)
	defer db.Close()
	schema := loadSchema(t, db)
	table := schema.Tables["posts"]

	// Column-to-column comparison: updated_at > created_at
	where := []map[string]any{
		{"updated_at": map[string]any{"gt": map[string]any{"__col": "created_at"}}},
	}

	sql, args, err := table.BuildWhereFromJSON(where, schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should produce column comparison, not parameterized value
	if !strings.Contains(sql, "[created_at]") {
		t.Errorf("SQL should reference created_at column: %s", sql)
	}
	if len(args) != 0 {
		t.Errorf("column reference should have 0 args, got %d", len(args))
	}
}

// =============================================================================
// SchemaCols Tests - Composite Primary Key Detection
// Criteria B: composite keys explicitly mentioned as edge case
// =============================================================================

func TestSchemaCols_CompositePrimaryKey(t *testing.T) {
	db := setupTestDB(t, schemaUserRoles)
	defer db.Close()

	tables, err := SchemaCols(db)
	if err != nil {
		t.Fatalf("SchemaCols error: %v", err)
	}

	tbl, ok := tables["user_roles"]
	if !ok {
		t.Fatal("user_roles table not found")
	}

	// Must have exactly 2 PK columns in correct order
	if len(tbl.Pk) != 2 {
		t.Fatalf("expected 2 PK columns, got %d: %v", len(tbl.Pk), tbl.Pk)
	}
	if tbl.Pk[0] != "user_id" || tbl.Pk[1] != "role_id" {
		t.Errorf("PK order wrong: got %v, want [user_id, role_id]", tbl.Pk)
	}
}

func TestSchemaCols_TripleCompositePrimaryKey(t *testing.T) {
	db := setupTestDB(t, schemaAuditLog)
	defer db.Close()

	tables, err := SchemaCols(db)
	if err != nil {
		t.Fatalf("SchemaCols error: %v", err)
	}

	tbl := tables["audit_log"]
	if len(tbl.Pk) != 3 {
		t.Fatalf("expected 3 PK columns, got %d: %v", len(tbl.Pk), tbl.Pk)
	}
	if tbl.Pk[0] != "tenant_id" || tbl.Pk[1] != "entity_type" || tbl.Pk[2] != "entity_id" {
		t.Errorf("PK order wrong: got %v", tbl.Pk)
	}
}

// =============================================================================
// parseJoinCondition Tests
// Criteria B: table.column format validation edge cases
// =============================================================================

func TestParseJoinCondition(t *testing.T) {
	tests := []struct {
		name    string
		cond    map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid",
			cond:    map[string]any{"users.id": map[string]any{"eq": "orders.user_id"}},
			wantErr: false,
		},
		{
			name:    "multiple keys error",
			cond:    map[string]any{"a.b": map[string]any{"eq": "c.d"}, "e.f": map[string]any{"eq": "g.h"}},
			wantErr: true,
			errMsg:  "exactly one key",
		},
		{
			name:    "no table prefix on left",
			cond:    map[string]any{"id": map[string]any{"eq": "orders.user_id"}},
			wantErr: true,
			errMsg:  "table.column format",
		},
		{
			name:    "no table prefix on right",
			cond:    map[string]any{"users.id": map[string]any{"eq": "user_id"}},
			wantErr: true,
			errMsg:  "table.column format",
		},
		{
			name:    "non-object value",
			cond:    map[string]any{"users.id": "orders.user_id"},
			wantErr: true,
			errMsg:  "must be an object",
		},
		{
			name:    "non-string right side",
			cond:    map[string]any{"users.id": map[string]any{"eq": 123}},
			wantErr: true,
			errMsg:  "column reference string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseJoinCondition(tt.cond)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got none")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error = %v, want containing %q", err, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// =============================================================================
// ParseSelectFromJSON Tests
// Criteria B: nested relations, aliases, edge cases
// =============================================================================

func TestParseSelectFromJSON(t *testing.T) {
	tests := []struct {
		name      string
		sel       []any
		wantCols  int
		wantJoins int
		wantErr   bool
	}{
		{"empty defaults to star", []any{}, 1, 0, false},
		{"simple columns", []any{"id", "name"}, 2, 0, false},
		{"star", []any{"*"}, 1, 0, false},
		{"aliased column", []any{map[string]any{"user_name": "name"}}, 1, 0, false},
		{"nested relation", []any{"id", map[string]any{"posts": []any{"title"}}}, 1, 1, false},
		{"deeply nested", []any{map[string]any{"posts": []any{"id", map[string]any{"comments": []any{"body"}}}}}, 1, 1, false},
		{"invalid type", []any{123}, 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, err := ParseSelectFromJSON(tt.sel, "users")

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(rel.columns) != tt.wantCols {
				t.Errorf("got %d columns, want %d", len(rel.columns), tt.wantCols)
			}
			if len(rel.joins) != tt.wantJoins {
				t.Errorf("got %d joins, want %d", len(rel.joins), tt.wantJoins)
			}
		})
	}
}

// =============================================================================
// BuildReturningFromJSON Tests
// Criteria B: edge cases for RETURNING clause
// =============================================================================

func TestBuildReturningFromJSON(t *testing.T) {
	db := setupTestDB(t, schemaUsers)
	defer db.Close()
	schema := loadSchema(t, db)
	table := schema.Tables["users"]

	tests := []struct {
		name    string
		cols    []string
		wantSQL string
		wantErr bool
	}{
		{"empty", []string{}, "", false},
		{"star", []string{"*"}, "RETURNING * ", false},
		{"single column", []string{"id"}, "RETURNING [id]", false},
		{"multiple columns", []string{"id", "name"}, "RETURNING [id], [name]", false},
		{"invalid column", []string{"nonexistent"}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, err := table.BuildReturningFromJSON(tt.cols)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantSQL != "" && !strings.Contains(sql, tt.wantSQL) {
				t.Errorf("SQL = %q, want containing %q", sql, tt.wantSQL)
			}
		})
	}
}

// =============================================================================
// Upsert Composite Primary Key Validation
// Criteria B: upsert requires all PK columns
// =============================================================================

func TestUpsertJSON_RequiresAllPKColumns(t *testing.T) {
	db := setupTestDB(t, schemaUserRoles)
	defer db.Close()
	schema := loadSchema(t, db)

	dao := &TenantConnection{
		Client: db,
		Schema: schema,
	}

	// Missing role_id from composite PK
	req := UpsertRequest{
		Data: []map[string]any{
			{"user_id": 1, "granted_at": "2024-01-01"},
		},
	}

	_, err := dao.UpsertJSON(context.Background(), "user_roles", req)
	if err == nil {
		t.Error("expected error for missing PK column")
	}
	if !strings.Contains(err.Error(), "role_id") {
		t.Errorf("error should mention missing column: %v", err)
	}
}

func TestUpsertJSON_AllPKColumnsPresent(t *testing.T) {
	db := setupTestDB(t, schemaUserRoles)
	defer db.Close()
	schema := loadSchema(t, db)

	dao := &TenantConnection{
		Client: db,
		Schema: schema,
	}

	// Both PK columns present
	req := UpsertRequest{
		Data: []map[string]any{
			{"user_id": 1, "role_id": 2, "granted_at": "2024-01-01"},
		},
	}

	result, err := dao.UpsertJSON(context.Background(), "user_roles", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["rows_affected"] != float64(1) {
		t.Errorf("expected 1 row affected, got %v", resp["rows_affected"])
	}
}

// =============================================================================
// Insert Primary Key Behavior
// Criteria B: plain insert may omit generated PK, upsert may not
// =============================================================================

func TestInsertJSON_AllowsOmittingAutoPrimaryKey(t *testing.T) {
	db := setupTestDB(t, schemaUsers)
	defer db.Close()
	schema := loadSchema(t, db)

	dao := &TenantConnection{
		Client: db,
		Schema: schema,
	}

	req := InsertRequest{
		Data: []map[string]any{
			{"name": "Alice", "email": "alice@example.com"},
		},
	}

	result, err := dao.InsertJSON(context.Background(), "users", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if _, ok := resp["last_insert_id"]; !ok {
		t.Fatalf("expected last_insert_id response, got %#v", resp)
	}

	var id int
	if err := db.QueryRow(`SELECT id FROM users WHERE email = 'alice@example.com'`).Scan(&id); err != nil {
		t.Fatalf("failed to load inserted row: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected generated primary key, got %d", id)
	}
}

func TestInsertJSON_MissingAutoPrimaryKeyDoesNotReturnUpsertError(t *testing.T) {
	db := setupTestDB(t, schemaUsers)
	defer db.Close()
	schema := loadSchema(t, db)

	dao := &TenantConnection{
		Client: db,
		Schema: schema,
	}

	req := InsertRequest{
		Data: []map[string]any{
			{"name": "Bob"},
		},
	}

	_, err := dao.InsertJSON(context.Background(), "users", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =============================================================================
// Update/Delete Require WHERE Clause
// Criteria B: validation edge case
// =============================================================================

func TestUpdateJSON_RequiresWhereClause(t *testing.T) {
	db := setupTestDB(t, schemaUsers)
	defer db.Close()
	schema := loadSchema(t, db)

	dao := &TenantConnection{
		Client: db,
		Schema: schema,
	}

	req := UpdateRequest{
		Data:  map[string]any{"status": "inactive"},
		Where: nil, // No WHERE clause
	}

	_, err := dao.UpdateJSON(context.Background(), "users", req)
	if err == nil {
		t.Error("expected error for missing WHERE clause")
	}
}

func TestDeleteJSON_RequiresWhereClause(t *testing.T) {
	db := setupTestDB(t, schemaUsers)
	defer db.Close()
	schema := loadSchema(t, db)

	dao := &TenantConnection{
		Client: db,
		Schema: schema,
	}

	req := DeleteRequest{
		Where: nil,
	}

	_, err := dao.DeleteJSON(context.Background(), "users", req)
	if err == nil {
		t.Error("expected error for missing WHERE clause")
	}
}

// =============================================================================
// Batch Transaction Atomicity
// Criteria C: complex context - transaction rollback
// =============================================================================

func TestBatch_TransactionRollback(t *testing.T) {
	db := setupTestDB(t, schemaUsers)
	defer db.Close()
	schema := loadSchema(t, db)

	dao := &TenantConnection{
		Client: db,
		Schema: schema,
	}

	// Batch with one valid insert, then invalid select (nonexistent table)
	// Should rollback the insert
	req := BatchRequest{
		Operations: []BatchOperation{
			{
				Operation: "insert",
				Table:     "users",
				Body:      map[string]any{"data": []any{map[string]any{"id": 1, "name": "Alice"}}},
			},
			{
				Operation: "select",
				Table:     "nonexistent",
				Body:      map[string]any{},
			},
		},
	}

	_, err := dao.Batch(context.Background(), req)
	if err == nil {
		t.Fatal("expected batch to fail")
	}

	// Verify rollback: user should NOT exist
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("failed to query users: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after rollback, got %d", count)
	}
}

// =============================================================================
// opToSQL Tests
// Criteria A: unlikely to change, operator mapping
// =============================================================================

func TestOpToSQL(t *testing.T) {
	tests := []struct {
		op   string
		want string
	}{
		{OpEq, "="},
		{OpNeq, "!="},
		{OpGt, ">"},
		{OpGte, ">="},
		{OpLt, "<"},
		{OpLte, "<="},
		{"unknown", "="}, // default
	}

	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			if got := opToSQL(tt.op); got != tt.want {
				t.Errorf("opToSQL(%q) = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}
