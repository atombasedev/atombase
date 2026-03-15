package platform

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// validateFKReferences Tests
// Criteria B: FK validation edge cases
// =============================================================================

func TestValidateFKReferences_Valid(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
		{Name: "posts", Columns: map[string]Col{
			"id":      {Name: "id", Type: "INTEGER"},
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users.id"},
		}},
	}}

	errors := validateFKReferences(schema)

	if len(errors) != 0 {
		t.Errorf("expected no errors for valid FK, got %d: %v", len(errors), errors)
	}
}

func TestValidateFKReferences_InvalidFormat(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "posts", Columns: map[string]Col{
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users"}, // missing .column
		}},
	}}

	errors := validateFKReferences(schema)

	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].Type != "fk_reference" {
		t.Errorf("type = %s, want fk_reference", errors[0].Type)
	}
	if !strings.Contains(errors[0].Message, "invalid foreign key format") {
		t.Errorf("message should mention invalid format: %s", errors[0].Message)
	}
}

func TestValidateFKReferences_MissingTable(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "posts", Columns: map[string]Col{
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users.id"}, // users table doesn't exist
		}},
	}}

	errors := validateFKReferences(schema)

	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if !strings.Contains(errors[0].Message, "non-existent table") {
		t.Errorf("message should mention non-existent table: %s", errors[0].Message)
	}
}

func TestValidateFKReferences_MissingColumn(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}},
		{Name: "posts", Columns: map[string]Col{
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users.user_id"}, // user_id doesn't exist in users
		}},
	}}

	errors := validateFKReferences(schema)

	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if !strings.Contains(errors[0].Message, "non-existent column") {
		t.Errorf("message should mention non-existent column: %s", errors[0].Message)
	}
}

func TestValidateFKReferences_MultipleErrors(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "posts", Columns: map[string]Col{
			"user_id":     {Name: "user_id", Type: "INTEGER", References: "users.id"},
			"category_id": {Name: "category_id", Type: "INTEGER", References: "categories.id"},
		}},
	}}

	errors := validateFKReferences(schema)

	if len(errors) != 2 {
		t.Errorf("expected 2 errors for missing tables, got %d", len(errors))
	}
}

func TestValidateFKReferences_NoFKs(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT"},
		}},
	}}

	errors := validateFKReferences(schema)

	if len(errors) != 0 {
		t.Errorf("expected no errors for schema without FKs, got %d", len(errors))
	}
}

// =============================================================================
// checkUniqueConstraint Tests
// Criteria C: Data-dependent validation
// =============================================================================

func TestCheckUniqueConstraint_NoDuplicates(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT);
		INSERT INTO users VALUES (1, 'a@test.com');
		INSERT INTO users VALUES (2, 'b@test.com');
		INSERT INTO users VALUES (3, 'c@test.com');
	`)
	defer db.Close()

	errors, err := checkUniqueConstraint(context.Background(), db, "users", "email")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(errors) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errors), errors)
	}
}

func TestCheckUniqueConstraint_WithDuplicates(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT);
		INSERT INTO users VALUES (1, 'a@test.com');
		INSERT INTO users VALUES (2, 'a@test.com');
		INSERT INTO users VALUES (3, 'b@test.com');
		INSERT INTO users VALUES (4, 'b@test.com');
		INSERT INTO users VALUES (5, 'b@test.com');
	`)
	defer db.Close()

	errors, err := checkUniqueConstraint(context.Background(), db, "users", "email")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].Type != "unique" {
		t.Errorf("type = %s, want unique", errors[0].Type)
	}
	if !strings.Contains(errors[0].Message, "duplicate values") {
		t.Errorf("message should mention duplicates: %s", errors[0].Message)
	}
}

func TestCheckUniqueConstraint_NullsIgnored(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT);
		INSERT INTO users VALUES (1, NULL);
		INSERT INTO users VALUES (2, NULL);
		INSERT INTO users VALUES (3, 'a@test.com');
	`)
	defer db.Close()

	errors, err := checkUniqueConstraint(context.Background(), db, "users", "email")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// NULL values should not count as duplicates
	if len(errors) != 0 {
		t.Errorf("expected no errors (NULLs don't count), got %d: %v", len(errors), errors)
	}
}

func TestCheckUniqueConstraint_ColumnNotExists(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
	`)
	defer db.Close()

	errors, err := checkUniqueConstraint(context.Background(), db, "users", "email")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Column doesn't exist, no data to check
	if len(errors) != 0 {
		t.Errorf("expected no errors for non-existent column, got %d", len(errors))
	}
}

// =============================================================================
// checkCheckConstraint Tests
// Criteria C: CHECK constraint validation
// =============================================================================

func TestCheckCheckConstraint_NoViolations(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, age INTEGER);
		INSERT INTO users VALUES (1, 25);
		INSERT INTO users VALUES (2, 30);
		INSERT INTO users VALUES (3, 18);
	`)
	defer db.Close()

	errors, err := checkCheckConstraint(context.Background(), db, "users", "age", "age >= 18")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(errors) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errors), errors)
	}
}

func TestCheckCheckConstraint_WithViolations(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, age INTEGER);
		INSERT INTO users VALUES (1, 25);
		INSERT INTO users VALUES (2, 15);
		INSERT INTO users VALUES (3, 10);
	`)
	defer db.Close()

	errors, err := checkCheckConstraint(context.Background(), db, "users", "age", "age >= 18")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].Type != "check" {
		t.Errorf("type = %s, want check", errors[0].Type)
	}
	if !strings.Contains(errors[0].Message, "2 rows violate") {
		t.Errorf("message should mention 2 violations: %s", errors[0].Message)
	}
}

func TestCheckCheckConstraint_ColumnNotExists(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
	`)
	defer db.Close()

	errors, err := checkCheckConstraint(context.Background(), db, "users", "age", "age >= 0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Column doesn't exist, no data to check
	if len(errors) != 0 {
		t.Errorf("expected no errors for non-existent column, got %d", len(errors))
	}
}

// =============================================================================
// checkFKConstraint Tests
// Criteria C: FK orphan row detection
// =============================================================================

func TestCheckFKConstraint_NoOrphans(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER);
		INSERT INTO users VALUES (1, 'Alice');
		INSERT INTO users VALUES (2, 'Bob');
		INSERT INTO posts VALUES (1, 1);
		INSERT INTO posts VALUES (2, 2);
	`)
	defer db.Close()

	col := Col{Name: "user_id", References: "users.id"}
	errors, err := checkFKConstraint(context.Background(), db, "posts", col)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(errors) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errors), errors)
	}
}

func TestCheckFKConstraint_WithOrphans(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER);
		INSERT INTO users VALUES (1, 'Alice');
		INSERT INTO posts VALUES (1, 1);
		INSERT INTO posts VALUES (2, 999);
		INSERT INTO posts VALUES (3, 888);
	`)
	defer db.Close()

	col := Col{Name: "user_id", References: "users.id"}
	errors, err := checkFKConstraint(context.Background(), db, "posts", col)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].Type != "fk_constraint" {
		t.Errorf("type = %s, want fk_constraint", errors[0].Type)
	}
	if !strings.Contains(errors[0].Message, "2 orphan rows") {
		t.Errorf("message should mention 2 orphans: %s", errors[0].Message)
	}
}

func TestCheckFKConstraint_NullsIgnored(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER);
		INSERT INTO users VALUES (1, 'Alice');
		INSERT INTO posts VALUES (1, 1);
		INSERT INTO posts VALUES (2, NULL);
		INSERT INTO posts VALUES (3, NULL);
	`)
	defer db.Close()

	col := Col{Name: "user_id", References: "users.id"}
	errors, err := checkFKConstraint(context.Background(), db, "posts", col)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// NULLs should not count as orphans
	if len(errors) != 0 {
		t.Errorf("expected no errors (NULLs don't count), got %d", len(errors))
	}
}

func TestCheckFKConstraint_ColumnNotExists(t *testing.T) {
	db := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT);
	`)
	defer db.Close()

	col := Col{Name: "user_id", References: "users.id"}
	errors, err := checkFKConstraint(context.Background(), db, "posts", col)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Column doesn't exist, no data to check
	if len(errors) != 0 {
		t.Errorf("expected no errors for non-existent column, got %d", len(errors))
	}
}

// =============================================================================
// AutoFixNotNullColumns Tests
// Criteria B: NOT NULL auto-fix logic
// =============================================================================

func TestAutoFixNotNullColumns_AddsDefaults(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":     {Name: "id", Type: "INTEGER"},
			"name":   {Name: "name", Type: "TEXT", NotNull: true},
			"age":    {Name: "age", Type: "INTEGER", NotNull: true},
			"score":  {Name: "score", Type: "REAL", NotNull: true},
			"data":   {Name: "data", Type: "BLOB", NotNull: true},
			"status": {Name: "status", Type: "TEXT", NotNull: true, Default: "active"}, // already has default
		}},
	}}

	changes := []SchemaDiff{
		{Type: "add_column", Table: "users", Column: "name"},
		{Type: "add_column", Table: "users", Column: "age"},
		{Type: "add_column", Table: "users", Column: "score"},
		{Type: "add_column", Table: "users", Column: "data"},
		{Type: "add_column", Table: "users", Column: "status"},
	}

	fixed := AutoFixNotNullColumns(schema, changes)

	// Check that defaults were added for columns without them
	users := fixed.Tables[0]

	if users.Columns["name"].Default != "" {
		t.Errorf("name default = %v, want empty string", users.Columns["name"].Default)
	}
	if users.Columns["age"].Default != 0 {
		t.Errorf("age default = %v, want 0", users.Columns["age"].Default)
	}
	if users.Columns["score"].Default != 0.0 {
		t.Errorf("score default = %v, want 0.0", users.Columns["score"].Default)
	}

	// status already had a default, should not be changed
	if users.Columns["status"].Default != "active" {
		t.Errorf("status default = %v, want 'active' (unchanged)", users.Columns["status"].Default)
	}
}

func TestAutoFixNotNullColumns_OnlyNewColumns(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT", NotNull: true}, // existing column
		}},
	}}

	// name is NOT in the changes (it's an existing column)
	changes := []SchemaDiff{}

	fixed := AutoFixNotNullColumns(schema, changes)

	// Existing columns should not get defaults added
	if fixed.Tables[0].Columns["name"].Default != nil {
		t.Errorf("existing column should not get default added: %v", fixed.Tables[0].Columns["name"].Default)
	}
}

func TestAutoFixNotNullColumns_NullableColumns(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"email": {Name: "email", Type: "TEXT", NotNull: false}, // nullable
		}},
	}}

	changes := []SchemaDiff{
		{Type: "add_column", Table: "users", Column: "email"},
	}

	fixed := AutoFixNotNullColumns(schema, changes)

	// Nullable columns should not get defaults
	if fixed.Tables[0].Columns["email"].Default != nil {
		t.Errorf("nullable column should not get default: %v", fixed.Tables[0].Columns["email"].Default)
	}
}

// =============================================================================
// getDefaultValue Tests
// Criteria A: Stable type mapping
// =============================================================================

func TestGetDefaultValue(t *testing.T) {
	tests := []struct {
		colType string
		want    any
	}{
		{"INTEGER", 0},
		{"integer", 0}, // case insensitive
		{"REAL", 0.0},
		{"real", 0.0},
		{"TEXT", ""},
		{"text", ""},
		{"BLOB", []byte{}},
		{"VARCHAR", ""}, // unknown types default to string
		{"CUSTOM", ""},
	}

	for _, tt := range tests {
		t.Run(tt.colType, func(t *testing.T) {
			result := getDefaultValue(tt.colType)

			// Special handling for []byte comparison
			if _, ok := tt.want.([]byte); ok {
				if _, ok := result.([]byte); !ok {
					t.Errorf("getDefaultValue(%s) = %T, want []byte", tt.colType, result)
				}
				return
			}

			if result != tt.want {
				t.Errorf("getDefaultValue(%s) = %v, want %v", tt.colType, result, tt.want)
			}
		})
	}
}

// =============================================================================
// ValidateMigrationPlan Integration Tests
// Criteria C: Full validation pipeline
// =============================================================================

func TestValidateMigrationPlan_AllValid(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER"},
			"name": {Name: "name", Type: "TEXT"},
		}},
	}}

	result, err := ValidateMigrationPlan(context.Background(), schema, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Valid {
		t.Errorf("expected valid result, got errors: %v", result.Errors)
	}
}

func TestValidateMigrationPlan_FKError(t *testing.T) {
	schema := Schema{Tables: []Table{
		{Name: "posts", Columns: map[string]Col{
			"user_id": {Name: "user_id", Type: "INTEGER", References: "users.id"}, // users doesn't exist
		}},
	}}

	result, err := ValidateMigrationPlan(context.Background(), schema, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Valid {
		t.Error("expected invalid result for FK error")
	}

	hasFKError := false
	for _, e := range result.Errors {
		if e.Type == "fk_reference" {
			hasFKError = true
			break
		}
	}
	if !hasFKError {
		t.Error("expected fk_reference error")
	}
}

func TestValidateMigrationPlan_WithProbeDB(t *testing.T) {
	probeDB := setupDataTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT);
		INSERT INTO users VALUES (1, 'a@test.com');
		INSERT INTO users VALUES (2, 'a@test.com');
	`)
	defer probeDB.Close()

	schema := Schema{Tables: []Table{
		{Name: "users", Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"email": {Name: "email", Type: "TEXT", Unique: true}, // would fail due to duplicates
		}},
	}}

	result, err := ValidateMigrationPlan(context.Background(), schema, probeDB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Valid {
		t.Error("expected invalid result for unique constraint violation")
	}

	hasUniqueError := false
	for _, e := range result.Errors {
		if e.Type == "unique" {
			hasUniqueError = true
			break
		}
	}
	if !hasUniqueError {
		t.Error("expected unique constraint error")
	}
}

func TestValidateMigrationExecution_LocalProbeSucceeds(t *testing.T) {
	current := Schema{Tables: []Table{
		{Name: "posts", Pk: []string{"id"}, Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"title": {Name: "title", Type: "TEXT"},
		}},
	}}

	err := ValidateMigrationExecution(context.Background(), current, []string{
		"ALTER TABLE [posts] RENAME COLUMN [title] TO [headline]",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMigrationExecution_LocalProbeFails(t *testing.T) {
	current := Schema{Tables: []Table{
		{Name: "posts", Pk: []string{"id"}, Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"title": {Name: "title", Type: "TEXT"},
		}},
	}}

	err := ValidateMigrationExecution(context.Background(), current, []string{
		"ALTER TABLE [posts] RENAME COLUMN [missing] TO [headline]",
	})
	if err == nil {
		t.Fatal("expected local probe to fail")
	}
	if !strings.Contains(err.Error(), "local migration probe failed") {
		t.Fatalf("expected local probe error, got %v", err)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// setupDataTestDB creates an in-memory database with the given schema/data.
func setupDataTestDB(t *testing.T, initSQL string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if _, err := db.Exec(initSQL); err != nil {
		db.Close()
		t.Fatalf("failed to setup db: %v", err)
	}
	return db
}
