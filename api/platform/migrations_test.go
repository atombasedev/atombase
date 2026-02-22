package platform

import (
	"context"
	"strings"
	"testing"
)

// =============================================================================
// applyMerges Tests
// Criteria B: Converts drop+add pairs to renames
// =============================================================================

func TestApplyMerges_TableRename(t *testing.T) {
	changes := []SchemaDiff{
		{Type: "drop_table", Table: "old_users"},
		{Type: "add_table", Table: "users"},
	}
	merges := []Merge{{Old: 0, New: 1}}

	renames := applyMerges(changes, merges)

	if len(renames) != 1 {
		t.Fatalf("expected 1 rename, got %d", len(renames))
	}
	if renames[0].Type != "rename_table" {
		t.Errorf("type = %s, want rename_table", renames[0].Type)
	}
	if renames[0].OldName != "old_users" || renames[0].NewName != "users" {
		t.Errorf("rename = %s -> %s, want old_users -> users", renames[0].OldName, renames[0].NewName)
	}
}

func TestApplyMerges_ColumnRename(t *testing.T) {
	changes := []SchemaDiff{
		{Type: "drop_column", Table: "users", Column: "name"},
		{Type: "add_column", Table: "users", Column: "full_name"},
	}
	merges := []Merge{{Old: 0, New: 1}}

	renames := applyMerges(changes, merges)

	if len(renames) != 1 {
		t.Fatalf("expected 1 rename, got %d", len(renames))
	}
	if renames[0].Type != "rename_column" {
		t.Errorf("type = %s, want rename_column", renames[0].Type)
	}
	if renames[0].Table != "users" {
		t.Errorf("table = %s, want users", renames[0].Table)
	}
	if renames[0].OldName != "name" || renames[0].NewName != "full_name" {
		t.Errorf("rename = %s -> %s, want name -> full_name", renames[0].OldName, renames[0].NewName)
	}
}

func TestApplyMerges_ColumnRenameDifferentTables(t *testing.T) {
	// Column rename only valid if same table
	changes := []SchemaDiff{
		{Type: "drop_column", Table: "users", Column: "name"},
		{Type: "add_column", Table: "posts", Column: "name"},
	}
	merges := []Merge{{Old: 0, New: 1}}

	renames := applyMerges(changes, merges)

	if len(renames) != 0 {
		t.Errorf("expected 0 renames for different tables, got %d", len(renames))
	}
}

func TestApplyMerges_InvalidIndices(t *testing.T) {
	changes := []SchemaDiff{
		{Type: "drop_table", Table: "users"},
	}
	merges := []Merge{
		{Old: -1, New: 0},  // negative index
		{Old: 0, New: 5},   // out of bounds
		{Old: 10, New: 11}, // both out of bounds
	}

	renames := applyMerges(changes, merges)

	if len(renames) != 0 {
		t.Errorf("expected 0 renames for invalid indices, got %d", len(renames))
	}
}

func TestApplyMerges_MultipleMerges(t *testing.T) {
	changes := []SchemaDiff{
		{Type: "drop_table", Table: "old_users"},
		{Type: "add_table", Table: "users"},
		{Type: "drop_column", Table: "posts", Column: "body"},
		{Type: "add_column", Table: "posts", Column: "content"},
	}
	merges := []Merge{
		{Old: 0, New: 1},
		{Old: 2, New: 3},
	}

	renames := applyMerges(changes, merges)

	if len(renames) != 2 {
		t.Fatalf("expected 2 renames, got %d", len(renames))
	}
}

// =============================================================================
// requiresMirrorTable Tests
// Criteria A: Stable detection logic
// =============================================================================

func TestRequiresMirrorTable(t *testing.T) {
	tests := []struct {
		name     string
		old      Col
		new      Col
		required bool
	}{
		{
			name:     "no change",
			old:      Col{Name: "email", Type: "TEXT"},
			new:      Col{Name: "email", Type: "TEXT"},
			required: false,
		},
		{
			name:     "type change only",
			old:      Col{Name: "age", Type: "INTEGER"},
			new:      Col{Name: "age", Type: "TEXT"},
			required: false, // type changes are metadata-only
		},
		{
			name:     "add FK",
			old:      Col{Name: "user_id", Type: "INTEGER"},
			new:      Col{Name: "user_id", Type: "INTEGER", References: "users.id"},
			required: true,
		},
		{
			name:     "modify FK reference",
			old:      Col{Name: "user_id", Type: "INTEGER", References: "users.id"},
			new:      Col{Name: "user_id", Type: "INTEGER", References: "accounts.id"},
			required: true,
		},
		{
			name:     "modify FK on delete",
			old:      Col{Name: "user_id", Type: "INTEGER", References: "users.id", OnDelete: "CASCADE"},
			new:      Col{Name: "user_id", Type: "INTEGER", References: "users.id", OnDelete: "SET NULL"},
			required: true,
		},
		{
			name:     "remove FK",
			old:      Col{Name: "user_id", Type: "INTEGER", References: "users.id"},
			new:      Col{Name: "user_id", Type: "INTEGER"},
			required: true,
		},
		{
			name:     "add CHECK",
			old:      Col{Name: "age", Type: "INTEGER"},
			new:      Col{Name: "age", Type: "INTEGER", Check: "age >= 0"},
			required: true,
		},
		{
			name:     "modify CHECK",
			old:      Col{Name: "age", Type: "INTEGER", Check: "age >= 0"},
			new:      Col{Name: "age", Type: "INTEGER", Check: "age >= 18"},
			required: true,
		},
		{
			name:     "remove CHECK",
			old:      Col{Name: "age", Type: "INTEGER", Check: "age >= 0"},
			new:      Col{Name: "age", Type: "INTEGER"},
			required: true,
		},
		{
			name:     "change COLLATE",
			old:      Col{Name: "name", Type: "TEXT", Collate: "BINARY"},
			new:      Col{Name: "name", Type: "TEXT", Collate: "NOCASE"},
			required: true,
		},
		{
			name:     "add generated",
			old:      Col{Name: "full_name", Type: "TEXT"},
			new:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || last"}},
			required: true,
		},
		{
			name:     "remove generated",
			old:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || last"}},
			new:      Col{Name: "full_name", Type: "TEXT"},
			required: true,
		},
		{
			name:     "change generated expr",
			old:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || last"}},
			new:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || ' ' || last"}},
			required: true,
		},
		{
			name:     "change generated stored",
			old:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || last", Stored: false}},
			new:      Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || last", Stored: true}},
			required: true,
		},
		{
			name:     "not null change",
			old:      Col{Name: "email", Type: "TEXT", NotNull: false},
			new:      Col{Name: "email", Type: "TEXT", NotNull: true},
			required: false, // handled by ALTER TABLE
		},
		{
			name:     "unique change",
			old:      Col{Name: "email", Type: "TEXT", Unique: false},
			new:      Col{Name: "email", Type: "TEXT", Unique: true},
			required: false, // handled by CREATE UNIQUE INDEX
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := requiresMirrorTable(tt.old, tt.new)
			if result != tt.required {
				t.Errorf("requiresMirrorTable() = %v, want %v", result, tt.required)
			}
		})
	}
}

// =============================================================================
// generateCreateTableSQL Tests
// Criteria B: Many column/constraint variations
// =============================================================================

func TestGenerateCreateTableSQL_BasicTable(t *testing.T) {
	table := Table{
		Name: "users",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT"},
		},
	}

	sql := generateCreateTableSQL(table)

	if !strings.Contains(sql, "CREATE TABLE [users]") {
		t.Errorf("missing CREATE TABLE: %s", sql)
	}
	if !strings.Contains(sql, "[id] INTEGER PRIMARY KEY") {
		t.Errorf("missing INTEGER PRIMARY KEY for single PK: %s", sql)
	}
	// name should be typeless (per design doc)
	if strings.Contains(sql, "[name] TEXT") {
		t.Errorf("non-PK column should be typeless: %s", sql)
	}
}

func TestGenerateCreateTableSQL_CompositePK(t *testing.T) {
	table := Table{
		Name: "user_roles",
		Pk:   []string{"user_id", "role_id"},
		Columns: map[string]Col{
			"user_id": {Name: "user_id", Type: "INTEGER"},
			"role_id": {Name: "role_id", Type: "INTEGER"},
		},
	}

	sql := generateCreateTableSQL(table)

	if !strings.Contains(sql, "PRIMARY KEY ([role_id], [user_id])") &&
		!strings.Contains(sql, "PRIMARY KEY ([user_id], [role_id])") {
		t.Errorf("missing composite PRIMARY KEY: %s", sql)
	}
	// Composite PK integer columns should have INTEGER type
	if !strings.Contains(sql, "[user_id] INTEGER") {
		t.Errorf("composite PK integer column should have type: %s", sql)
	}
}

func TestGenerateCreateTableSQL_WithConstraints(t *testing.T) {
	table := Table{
		Name: "users",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"email": {Name: "email", Type: "TEXT", NotNull: true, Unique: true},
			"age":   {Name: "age", Type: "INTEGER", Check: "age >= 0"},
		},
	}

	sql := generateCreateTableSQL(table)

	if !strings.Contains(sql, "NOT NULL") {
		t.Errorf("missing NOT NULL: %s", sql)
	}
	if !strings.Contains(sql, "UNIQUE") {
		t.Errorf("missing UNIQUE: %s", sql)
	}
	if !strings.Contains(sql, "CHECK (age >= 0)") {
		t.Errorf("missing CHECK: %s", sql)
	}
}

func TestGenerateCreateTableSQL_WithDefault(t *testing.T) {
	table := Table{
		Name: "users",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":     {Name: "id", Type: "INTEGER"},
			"status": {Name: "status", Type: "TEXT", Default: "active"},
			"count":  {Name: "count", Type: "INTEGER", Default: 0},
		},
	}

	sql := generateCreateTableSQL(table)

	if !strings.Contains(sql, "DEFAULT 'active'") {
		t.Errorf("missing string default: %s", sql)
	}
	if !strings.Contains(sql, "DEFAULT 0") {
		t.Errorf("missing integer default: %s", sql)
	}
}

func TestGenerateCreateTableSQL_WithFK(t *testing.T) {
	table := Table{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":      {Name: "id", Type: "INTEGER"},
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users.id", OnDelete: "CASCADE"},
		},
	}

	sql := generateCreateTableSQL(table)

	if !strings.Contains(sql, "FOREIGN KEY ([user_id]) REFERENCES [users]([id])") {
		t.Errorf("missing FOREIGN KEY: %s", sql)
	}
	if !strings.Contains(sql, "ON DELETE CASCADE") {
		t.Errorf("missing ON DELETE CASCADE: %s", sql)
	}
}

func TestGenerateCreateTableSQL_WithGenerated(t *testing.T) {
	table := Table{
		Name: "users",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":        {Name: "id", Type: "INTEGER"},
			"first":     {Name: "first", Type: "TEXT"},
			"last":      {Name: "last", Type: "TEXT"},
			"full_name": {Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || ' ' || last", Stored: true}},
		},
	}

	sql := generateCreateTableSQL(table)

	if !strings.Contains(sql, "GENERATED ALWAYS AS (first || ' ' || last) STORED") {
		t.Errorf("missing GENERATED column: %s", sql)
	}
}

// =============================================================================
// generateAddColumnSQL Tests
// Criteria B: NOT NULL auto-fix edge cases
// =============================================================================

func TestGenerateAddColumnSQL_Simple(t *testing.T) {
	col := Col{Name: "email", Type: "TEXT"}
	sql := generateAddColumnSQL("users", col)

	if sql != "ALTER TABLE [users] ADD COLUMN [email]" {
		t.Errorf("sql = %s, want ALTER TABLE [users] ADD COLUMN [email]", sql)
	}
}

func TestGenerateAddColumnSQL_WithDefault(t *testing.T) {
	col := Col{Name: "status", Type: "TEXT", Default: "active"}
	sql := generateAddColumnSQL("users", col)

	if !strings.Contains(sql, "DEFAULT 'active'") {
		t.Errorf("missing default: %s", sql)
	}
}

func TestGenerateAddColumnSQL_NotNullWithDefault(t *testing.T) {
	col := Col{Name: "count", Type: "INTEGER", NotNull: true, Default: 0}
	sql := generateAddColumnSQL("users", col)

	if !strings.Contains(sql, "NOT NULL") {
		t.Errorf("missing NOT NULL: %s", sql)
	}
	if !strings.Contains(sql, "DEFAULT 0") {
		t.Errorf("missing default: %s", sql)
	}
}

func TestGenerateAddColumnSQL_NotNullAutoFix(t *testing.T) {
	tests := []struct {
		colType     string
		wantDefault string
	}{
		{"INTEGER", "DEFAULT 0"},
		{"REAL", "DEFAULT 0"},
		{"TEXT", "DEFAULT ''"},
		{"BLOB", "DEFAULT X''"},
		{"VARCHAR", "DEFAULT ''"}, // unknown type defaults to empty string
	}

	for _, tt := range tests {
		t.Run(tt.colType, func(t *testing.T) {
			col := Col{Name: "test", Type: tt.colType, NotNull: true}
			sql := generateAddColumnSQL("users", col)

			if !strings.Contains(sql, tt.wantDefault) {
				t.Errorf("sql = %s, want %s", sql, tt.wantDefault)
			}
		})
	}
}

// =============================================================================
// generateMirrorTableSQL Tests
// Criteria C: Complex multi-step operation
// =============================================================================

func TestGenerateMirrorTableSQL(t *testing.T) {
	oldTable := Table{
		Name: "users",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT"},
		},
	}
	newTable := Table{
		Name: "users",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT", Check: "length(name) > 0"},
		},
	}

	statements := generateMirrorTableSQL(oldTable, newTable)

	if len(statements) != 4 {
		t.Fatalf("expected 4 statements, got %d", len(statements))
	}

	// 1. CREATE TABLE users_new
	if !strings.Contains(statements[0], "CREATE TABLE [users_new]") {
		t.Errorf("statement 0 should create temp table: %s", statements[0])
	}

	// 2. INSERT INTO users_new SELECT FROM users
	if !strings.Contains(statements[1], "INSERT INTO [users_new]") &&
		!strings.Contains(statements[1], "FROM [users]") {
		t.Errorf("statement 1 should copy data: %s", statements[1])
	}

	// 3. DROP TABLE users
	if !strings.Contains(statements[2], "DROP TABLE [users]") {
		t.Errorf("statement 2 should drop old table: %s", statements[2])
	}

	// 4. RENAME users_new TO users
	if !strings.Contains(statements[3], "RENAME TO [users]") {
		t.Errorf("statement 3 should rename: %s", statements[3])
	}
}

// =============================================================================
// generateCreateIndexSQL Tests
// Criteria A: Simple, stable function
// =============================================================================

func TestGenerateCreateIndexSQL(t *testing.T) {
	tests := []struct {
		name    string
		index   Index
		wantSQL string
	}{
		{
			name:    "simple index",
			index:   Index{Name: "idx_email", Columns: []string{"email"}},
			wantSQL: "CREATE INDEX IF NOT EXISTS [idx_email] ON [users] ([email])",
		},
		{
			name:    "unique index",
			index:   Index{Name: "idx_email", Columns: []string{"email"}, Unique: true},
			wantSQL: "CREATE UNIQUE INDEX IF NOT EXISTS [idx_email] ON [users] ([email])",
		},
		{
			name:    "composite index",
			index:   Index{Name: "idx_name", Columns: []string{"first", "last"}},
			wantSQL: "CREATE INDEX IF NOT EXISTS [idx_name] ON [users] ([first], [last])",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := generateCreateIndexSQL("users", tt.index)
			if sql != tt.wantSQL {
				t.Errorf("sql = %s, want %s", sql, tt.wantSQL)
			}
		})
	}
}

// =============================================================================
// generateFTSSQL Tests
// Criteria B: FTS5 virtual table + triggers
// =============================================================================

func TestGenerateFTSSQL(t *testing.T) {
	ftsColumns := []string{"title", "body"}
	pk := []string{"id"}

	statements := generateFTSSQL("posts", ftsColumns, pk)

	if len(statements) != 4 {
		t.Fatalf("expected 4 statements (create + 3 triggers), got %d", len(statements))
	}

	// CREATE VIRTUAL TABLE
	if !strings.Contains(statements[0], "CREATE VIRTUAL TABLE") ||
		!strings.Contains(statements[0], "posts_fts") ||
		!strings.Contains(statements[0], "fts5") {
		t.Errorf("statement 0 should create FTS5 table: %s", statements[0])
	}

	// Insert trigger
	if !strings.Contains(statements[1], "AFTER INSERT") {
		t.Errorf("statement 1 should be insert trigger: %s", statements[1])
	}

	// Delete trigger
	if !strings.Contains(statements[2], "AFTER DELETE") {
		t.Errorf("statement 2 should be delete trigger: %s", statements[2])
	}

	// Update trigger
	if !strings.Contains(statements[3], "AFTER UPDATE") {
		t.Errorf("statement 3 should be update trigger: %s", statements[3])
	}
}

func TestGenerateDropFTSSQL(t *testing.T) {
	statements := generateDropFTSSQL("posts")

	if len(statements) != 4 {
		t.Fatalf("expected 4 statements, got %d", len(statements))
	}

	// Should drop triggers first, then table
	for i := 0; i < 3; i++ {
		if !strings.Contains(statements[i], "DROP TRIGGER") {
			t.Errorf("statement %d should drop trigger: %s", i, statements[i])
		}
	}
	if !strings.Contains(statements[3], "DROP TABLE") {
		t.Errorf("statement 3 should drop table: %s", statements[3])
	}
}

// =============================================================================
// formatDefault Tests
// Criteria A: Stable formatting function
// =============================================================================

func TestFormatDefault(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"string", "active", "'active'"},
		{"string with quote", "it's", "'it''s'"}, // SQL escaping
		{"integer", 42, "42"},
		{"float", 3.14, "3.14"},
		{"bool true", true, "1"},
		{"bool false", false, "0"},
		{"nil", nil, "NULL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDefault(tt.val)
			if result != tt.want {
				t.Errorf("formatDefault(%v) = %s, want %s", tt.val, result, tt.want)
			}
		})
	}
}

// =============================================================================
// getDefaultForType Tests
// Criteria A: Type-appropriate defaults for NOT NULL auto-fix
// =============================================================================

func TestGetDefaultForType(t *testing.T) {
	tests := []struct {
		colType string
		want    string
	}{
		{"INTEGER", "0"},
		{"integer", "0"}, // case insensitive
		{"REAL", "0"},
		{"TEXT", "''"},
		{"BLOB", "X''"},
		{"VARCHAR", "''"}, // unknown defaults to empty string
		{"CUSTOM", "''"},  // unknown defaults to empty string
	}

	for _, tt := range tests {
		t.Run(tt.colType, func(t *testing.T) {
			result := getDefaultForType(tt.colType)
			if result != tt.want {
				t.Errorf("getDefaultForType(%s) = %s, want %s", tt.colType, result, tt.want)
			}
		})
	}
}

// =============================================================================
// GenerateMigrationPlan Tests
// Criteria C: Integration test for full plan generation
// =============================================================================

func TestGenerateMigrationPlan_AddTable(t *testing.T) {
	oldSchema := Schema{Tables: []Table{}}
	newSchema := Schema{Tables: []Table{
		{Name: "users", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	changes := []SchemaDiff{{Type: "add_table", Table: "users"}}

	plan, err := GenerateMigrationPlan(oldSchema, newSchema, changes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.SQL) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(plan.SQL))
	}
	if !strings.Contains(plan.SQL[0], "CREATE TABLE [users]") {
		t.Errorf("expected CREATE TABLE: %s", plan.SQL[0])
	}
}

func TestGenerateMigrationPlan_DropTable(t *testing.T) {
	oldSchema := Schema{Tables: []Table{
		{Name: "users", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	newSchema := Schema{Tables: []Table{}}
	changes := []SchemaDiff{{Type: "drop_table", Table: "users"}}

	plan, err := GenerateMigrationPlan(oldSchema, newSchema, changes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.SQL) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(plan.SQL))
	}
	if !strings.Contains(plan.SQL[0], "DROP TABLE") {
		t.Errorf("expected DROP TABLE: %s", plan.SQL[0])
	}
}

func TestGenerateMigrationPlan_RenameTable(t *testing.T) {
	oldSchema := Schema{Tables: []Table{
		{Name: "old_users", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	newSchema := Schema{Tables: []Table{
		{Name: "users", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	changes := []SchemaDiff{
		{Type: "drop_table", Table: "old_users"},
		{Type: "add_table", Table: "users"},
	}
	merges := []Merge{{Old: 0, New: 1}}

	plan, err := GenerateMigrationPlan(oldSchema, newSchema, changes, merges)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.SQL) != 1 {
		t.Fatalf("expected 1 statement, got %d: %v", len(plan.SQL), plan.SQL)
	}
	if !strings.Contains(plan.SQL[0], "RENAME TO") {
		t.Errorf("expected RENAME TO: %s", plan.SQL[0])
	}
}

func TestGenerateMigrationPlan_AddColumn(t *testing.T) {
	oldSchema := Schema{Tables: []Table{
		{Name: "users", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	newSchema := Schema{Tables: []Table{
		{Name: "users", Pk: []string{"id"}, Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"email": {Name: "email", Type: "TEXT"},
		}},
	}}
	changes := []SchemaDiff{{Type: "add_column", Table: "users", Column: "email"}}

	plan, err := GenerateMigrationPlan(oldSchema, newSchema, changes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.SQL) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(plan.SQL))
	}
	if !strings.Contains(plan.SQL[0], "ADD COLUMN") {
		t.Errorf("expected ADD COLUMN: %s", plan.SQL[0])
	}
}

func TestGenerateMigrationPlan_SQLOrdering(t *testing.T) {
	// Test that SQL follows correct order: renames, adds, drops
	oldSchema := Schema{Tables: []Table{
		{Name: "old_table", Pk: []string{"id"}, Columns: map[string]Col{
			"id":       {Name: "id", Type: "INTEGER"},
			"old_name": {Name: "old_name", Type: "TEXT"},
		}},
		{Name: "to_drop", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
	}}
	newSchema := Schema{Tables: []Table{
		{Name: "new_table", Pk: []string{"id"}, Columns: map[string]Col{
			"id":       {Name: "id", Type: "INTEGER"},
			"new_name": {Name: "new_name", Type: "TEXT"},
			"added":    {Name: "added", Type: "TEXT"},
		}},
	}}
	changes := []SchemaDiff{
		{Type: "drop_table", Table: "old_table"},
		{Type: "add_table", Table: "new_table"},
		{Type: "drop_table", Table: "to_drop"},
		{Type: "drop_column", Table: "old_table", Column: "old_name"},
		{Type: "add_column", Table: "new_table", Column: "new_name"},
		{Type: "add_column", Table: "new_table", Column: "added"},
	}
	merges := []Merge{
		{Old: 0, New: 1}, // table rename
		{Old: 3, New: 4}, // column rename
	}

	plan, err := GenerateMigrationPlan(oldSchema, newSchema, changes, merges)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find indices of different operation types
	var renameIdx, addIdx, dropIdx int = -1, -1, -1
	for i, sql := range plan.SQL {
		if strings.Contains(sql, "RENAME") && renameIdx == -1 {
			renameIdx = i
		}
		if strings.Contains(sql, "ADD COLUMN") && addIdx == -1 {
			addIdx = i
		}
		if strings.Contains(sql, "DROP TABLE") && dropIdx == -1 {
			dropIdx = i
		}
	}

	// Verify order: renames < adds < drops
	if renameIdx > addIdx && addIdx != -1 {
		t.Error("renames should come before adds")
	}
	if addIdx > dropIdx && dropIdx != -1 {
		t.Error("adds should come before drops")
	}
}

// =============================================================================
// Migration CRUD Tests
// Criteria C: Database operations
// =============================================================================

func TestCreateMigration(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create a template first (FK constraint)
	_, err := conn.Exec(`INSERT INTO atomicbase_schema_templates (name) VALUES ('test')`)
	if err != nil {
		t.Fatalf("failed to create template: %v", err)
	}

	migration, err := CreateMigration(context.Background(), 1, 1, 2, []string{"ALTER TABLE users ADD COLUMN email"})
	if err != nil {
		t.Fatalf("CreateMigration failed: %v", err)
	}

	if migration.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if migration.TemplateID != 1 {
		t.Errorf("templateId = %d, want 1", migration.TemplateID)
	}
	if migration.FromVersion != 1 || migration.ToVersion != 2 {
		t.Errorf("versions = %d->%d, want 1->2", migration.FromVersion, migration.ToVersion)
	}
	if migration.Status != "ready" {
		t.Errorf("status = %s, want ready", migration.Status)
	}
	if len(migration.SQL) != 1 {
		t.Errorf("sql count = %d, want 1", len(migration.SQL))
	}
}

func TestGetMigration(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create template and migration
	_, err := conn.Exec(`INSERT INTO atomicbase_schema_templates (name) VALUES ('test')`)
	if err != nil {
		t.Fatalf("failed to create template: %v", err)
	}

	created, err := CreateMigration(context.Background(), 1, 1, 2, []string{"SELECT 1"})
	if err != nil {
		t.Fatalf("CreateMigration failed: %v", err)
	}

	// Get it back
	migration, err := GetMigration(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetMigration failed: %v", err)
	}

	if migration.ID != created.ID {
		t.Errorf("id = %d, want %d", migration.ID, created.ID)
	}
	if migration.Status != "ready" {
		t.Errorf("status = %s, want ready", migration.Status)
	}
}

func TestGetMigration_NotFound(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	_, err := GetMigration(context.Background(), 999)
	if err == nil {
		t.Error("expected error for non-existent migration")
	}
}

func TestUpdateMigrationStatus(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create template and migration
	_, err := conn.Exec(`INSERT INTO atomicbase_schema_templates (name) VALUES ('test')`)
	if err != nil {
		t.Fatalf("failed to create template: %v", err)
	}

	migration, err := CreateMigration(context.Background(), 1, 1, 2, []string{"SELECT 1"})
	if err != nil {
		t.Fatalf("CreateMigration failed: %v", err)
	}

	// Update status
	state := MigrationStateSuccess
	err = UpdateMigrationStatus(context.Background(), migration.ID, MigrationStatusComplete, &state, 10, 0)
	if err != nil {
		t.Fatalf("UpdateMigrationStatus failed: %v", err)
	}

	// Verify
	updated, err := GetMigration(context.Background(), migration.ID)
	if err != nil {
		t.Fatalf("GetMigration failed: %v", err)
	}

	if updated.Status != MigrationStatusComplete {
		t.Errorf("status = %s, want complete", updated.Status)
	}
	if updated.State != nil {
		t.Errorf("state = %v, want nil", updated.State)
	}
}

func TestStartMigration(t *testing.T) {
	conn := setupTestDB(t)
	defer cleanupTestDB(t)
	defer conn.Close()

	// Create template and migration
	_, err := conn.Exec(`INSERT INTO atomicbase_schema_templates (name) VALUES ('test')`)
	if err != nil {
		t.Fatalf("failed to create template: %v", err)
	}

	migration, err := CreateMigration(context.Background(), 1, 1, 2, []string{"SELECT 1"})
	if err != nil {
		t.Fatalf("CreateMigration failed: %v", err)
	}

	// Start it
	err = StartMigration(context.Background(), migration.ID, 25)
	if err != nil {
		t.Fatalf("StartMigration failed: %v", err)
	}

	// Verify
	started, err := GetMigration(context.Background(), migration.ID)
	if err != nil {
		t.Fatalf("GetMigration failed: %v", err)
	}

	if started.Status != MigrationStatusRunning {
		t.Errorf("status = %s, want running", started.Status)
	}
	if started.TotalDBs != 0 {
		t.Errorf("totalDbs = %d, want 0", started.TotalDBs)
	}
}
