package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/atombasedev/atombase/primarystore"
	"github.com/atombasedev/atombase/tools"
	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// Test Database Setup
// =============================================================================

// setupTenantTestDB creates a test database with all required tables.
func setupTenantTestDB(t *testing.T) *sql.DB {
	t.Helper()

	testDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	// Create templates table
	_, err = testDB.Exec(`
		CREATE TABLE IF NOT EXISTS ` + TableTemplates + ` (
			id INTEGER PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			current_version INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	if err != nil {
		testDB.Close()
		t.Fatalf("failed to create templates table: %v", err)
	}

	// Create templates history table
	_, err = testDB.Exec(`
		CREATE TABLE IF NOT EXISTS ` + TableTemplatesHistory + ` (
			id INTEGER PRIMARY KEY,
			template_id INTEGER NOT NULL,
			version INTEGER NOT NULL,
			schema BLOB NOT NULL,
			checksum TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(template_id, version)
		)
	`)
	if err != nil {
		testDB.Close()
		t.Fatalf("failed to create templates_history table: %v", err)
	}

	// Create databases table
	_, err = testDB.Exec(`
		CREATE TABLE IF NOT EXISTS ` + TableDatabases + ` (
			id INTEGER PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			template_id INTEGER NOT NULL,
			template_version INTEGER NOT NULL,
			auth_token_encrypted BLOB,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	if err != nil {
		testDB.Close()
		t.Fatalf("failed to create databases table: %v", err)
	}

	// Create migrations table
	_, err = testDB.Exec(`
		CREATE TABLE IF NOT EXISTS ` + TableMigrations + ` (
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
		)
	`)
	if err != nil {
		testDB.Close()
		t.Fatalf("failed to create migrations table: %v", err)
	}

	// Create migration_failures table
	_, err = testDB.Exec(`
		CREATE TABLE IF NOT EXISTS ` + TableMigrationFailures + ` (
			database_id INTEGER PRIMARY KEY,
			from_version INTEGER NOT NULL,
			to_version INTEGER NOT NULL,
			error TEXT,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		testDB.Close()
		t.Fatalf("failed to create migration_failures table: %v", err)
	}

	return testDB
}

// setTestDB temporarily sets the package-level API bridge for testing.
func setTestDB(t *testing.T, testDB *sql.DB) func() {
	t.Helper()
	ps, err := primarystore.New(testDB)
	if err != nil {
		t.Fatalf("failed to create primary store: %v", err)
	}
	oldAPI := currentTestAPI
	currentTestAPI = &API{store: ps}

	return func() {
		_ = ps.Close()
		currentTestAPI = oldAPI
	}
}

// insertTestTemplate inserts a template for testing.
func insertTestTemplate(t *testing.T, testDB *sql.DB, name string, version int) int32 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	schema := Schema{Tables: []Table{{Name: "users", Pk: []string{"id"}, Columns: map[string]Col{"id": {Name: "id", Type: "INTEGER"}}}}}
	schemaJSON, _ := tools.EncodeSchema(schema)

	result, err := testDB.Exec(`
		INSERT INTO `+TableTemplates+` (name, current_version, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, name, version, now, now)
	if err != nil {
		t.Fatalf("failed to insert template: %v", err)
	}

	id, _ := result.LastInsertId()

	// Insert history for all versions up to current
	for v := 1; v <= version; v++ {
		_, err = testDB.Exec(`
			INSERT INTO `+TableTemplatesHistory+` (template_id, version, schema, checksum, created_at)
			VALUES (?, ?, ?, 'test', ?)
		`, id, v, schemaJSON, now)
		if err != nil {
			t.Fatalf("failed to insert template history: %v", err)
		}
	}

	return int32(id)
}

// insertTestTenant inserts a database for testing.
func insertTestTenant(t *testing.T, testDB *sql.DB, name string, templateID int32, version int) int32 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)

	result, err := testDB.Exec(`
		INSERT INTO `+TableDatabases+` (name, template_id, template_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, name, templateID, version, now, now)
	if err != nil {
		t.Fatalf("failed to insert database: %v", err)
	}

	id, _ := result.LastInsertId()
	return int32(id)
}

// insertTestMigration inserts a migration for testing.
func insertTestMigration(t *testing.T, testDB *sql.DB, templateID int32, fromVersion, toVersion int) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	sqlJSON, _ := json.Marshal([]string{"ALTER TABLE users ADD COLUMN name TEXT"})

	result, err := testDB.Exec(`
		INSERT INTO `+TableMigrations+` (template_id, from_version, to_version, sql, status, created_at)
		VALUES (?, ?, ?, ?, 'complete', ?)
	`, templateID, fromVersion, toVersion, string(sqlJSON), now)
	if err != nil {
		t.Fatalf("failed to insert migration: %v", err)
	}

	id, _ := result.LastInsertId()
	return id
}

// =============================================================================
// ListDatabases Tests
// Criteria B: Various database states
// =============================================================================

func TestListDatabases_Empty(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	databases, err := currentTestAPI.listDatabases(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(databases) != 0 {
		t.Errorf("expected empty list, got %d databases", len(databases))
	}
}

func TestListDatabases_Multiple(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 1)
	insertTestTenant(t, testDB, "database-a", templateID, 1)
	insertTestTenant(t, testDB, "database-b", templateID, 1)
	insertTestTenant(t, testDB, "database-c", templateID, 1)

	databases, err := currentTestAPI.listDatabases(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(databases) != 3 {
		t.Fatalf("expected 3 databases, got %d", len(databases))
	}

	// Should be ordered by name
	if databases[0].Name != "database-a" || databases[1].Name != "database-b" || databases[2].Name != "database-c" {
		t.Errorf("databases not in expected order: %v", databases)
	}
}

// =============================================================================
// GetDatabase Tests
// Criteria B: Various scenarios
// =============================================================================

func TestGetDatabase_Found(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	insertTestTenant(t, testDB, "my-database", templateID, 2)

	database, err := currentTestAPI.getDatabase(context.Background(), "my-database")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if database.Name != "my-database" {
		t.Errorf("name = %s, want my-database", database.Name)
	}
	if database.TemplateID != templateID {
		t.Errorf("templateID = %d, want %d", database.TemplateID, templateID)
	}
	if database.TemplateVersion != 2 {
		t.Errorf("templateVersion = %d, want 2", database.TemplateVersion)
	}
}

func TestGetDatabase_NotFound(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	_, err := currentTestAPI.getDatabase(context.Background(), "nonexistent")
	if err != ErrDatabaseNotFound {
		t.Errorf("expected ErrDatabaseNotFound, got: %v", err)
	}
}

// =============================================================================
// GetMigrationByVersions Tests
// Criteria A: Core function for chained migrations
// =============================================================================

func TestGetMigrationByVersions_Found(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	insertTestMigration(t, testDB, templateID, 1, 2)

	migration, err := currentTestAPI.getMigrationByVersions(context.Background(), templateID, 1, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if migration.FromVersion != 1 {
		t.Errorf("fromVersion = %d, want 1", migration.FromVersion)
	}
	if migration.ToVersion != 2 {
		t.Errorf("toVersion = %d, want 2", migration.ToVersion)
	}
	if len(migration.SQL) != 1 {
		t.Errorf("expected 1 SQL statement, got %d", len(migration.SQL))
	}
}

func TestGetMigrationByVersions_NotFound(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 1)

	_, err := currentTestAPI.getMigrationByVersions(context.Background(), templateID, 1, 2)
	if err == nil {
		t.Error("expected error for missing migration")
	}
}

// =============================================================================
// GetDatabasesByTemplate Tests
// Criteria C: Complex context - relationship between databases and templates
// =============================================================================

func TestGetDatabasesByTemplate_Found(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	template1 := insertTestTemplate(t, testDB, "app1", 1)
	template2 := insertTestTemplate(t, testDB, "app2", 1)

	insertTestTenant(t, testDB, "database-1a", template1, 1)
	insertTestTenant(t, testDB, "database-1b", template1, 1)
	insertTestTenant(t, testDB, "database-2a", template2, 1)

	databases, err := currentTestAPI.getDatabasesByTemplate(context.Background(), template1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(databases) != 2 {
		t.Fatalf("expected 2 databases for template1, got %d", len(databases))
	}

	for _, database := range databases {
		if database.TemplateID != template1 {
			t.Errorf("database %s has wrong templateID: %d", database.Name, database.TemplateID)
		}
	}
}

func TestGetDatabasesByTemplate_Empty(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "unused", 1)

	databases, err := currentTestAPI.getDatabasesByTemplate(context.Background(), templateID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(databases) != 0 {
		t.Errorf("expected 0 databases, got %d", len(databases))
	}
}

// =============================================================================
// GetPendingDatabases Tests
// Criteria C: Complex query with version filtering
// =============================================================================

func TestGetPendingDatabases_AllPending(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	migrationID := insertTestMigration(t, testDB, templateID, 1, 2)

	insertTestTenant(t, testDB, "database-1", templateID, 1)
	insertTestTenant(t, testDB, "database-2", templateID, 1)
	insertTestTenant(t, testDB, "database-3", templateID, 1)

	pending, err := currentTestAPI.getPendingDatabases(context.Background(), migrationID, templateID, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pending) != 3 {
		t.Errorf("expected 3 pending databases, got %d", len(pending))
	}
}

func TestGetPendingDatabases_SomeAlreadyAtTarget(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	migrationID := insertTestMigration(t, testDB, templateID, 1, 2)

	insertTestTenant(t, testDB, "database-1", templateID, 1)
	insertTestTenant(t, testDB, "database-2", templateID, 1)
	insertTestTenant(t, testDB, "database-3", templateID, 2) // Already at version 2

	pending, err := currentTestAPI.getPendingDatabases(context.Background(), migrationID, templateID, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// database-1 and database-2 should be pending (database-3 already at current version)
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending databases, got %d", len(pending))
	}
	if pending[0].Name != "database-1" {
		t.Errorf("expected database-1, got %s", pending[0].Name)
	}
	if pending[1].Name != "database-2" {
		t.Errorf("expected database-2, got %s", pending[1].Name)
	}
}

func TestGetPendingDatabases_NoPending(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	migrationID := insertTestMigration(t, testDB, templateID, 1, 2)

	// All databases already at version 2
	insertTestTenant(t, testDB, "database-1", templateID, 2)
	insertTestTenant(t, testDB, "database-2", templateID, 2)

	pending, err := currentTestAPI.getPendingDatabases(context.Background(), migrationID, templateID, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pending) != 0 {
		t.Errorf("expected 0 pending databases, got %d", len(pending))
	}
}

// =============================================================================
// GetFailedDatabases Tests
// Criteria C: Join query for migration failures
// =============================================================================

func TestGetFailedDatabases_SomeFailed(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	migrationID := insertTestMigration(t, testDB, templateID, 1, 2)

	tenant1 := insertTestTenant(t, testDB, "database-1", templateID, 1)
	tenant2 := insertTestTenant(t, testDB, "database-2", templateID, 1)
	tenant3 := insertTestTenant(t, testDB, "database-3", templateID, 1)

	now := time.Now().UTC().Format(time.RFC3339)

	// database-2: failed
	_, _ = testDB.Exec(`INSERT INTO `+TableMigrationFailures+` (database_id, from_version, to_version, error, created_at) VALUES (?, 1, 2, 'connection error', ?)`, tenant2, now)
	// database-3: failed
	_, _ = testDB.Exec(`INSERT INTO `+TableMigrationFailures+` (database_id, from_version, to_version, error, created_at) VALUES (?, 1, 2, 'timeout', ?)`, tenant3, now)
	// different version failure should not match this job
	_, _ = testDB.Exec(`INSERT INTO `+TableMigrationFailures+` (database_id, from_version, to_version, error, created_at) VALUES (?, 2, 3, 'old failure', ?)`, tenant1, now)

	failed, err := currentTestAPI.getFailedDatabases(context.Background(), migrationID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(failed) != 2 {
		t.Fatalf("expected 2 failed databases, got %d", len(failed))
	}
}

func TestGetFailedDatabases_NoneProcessed(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	migrationID := insertTestMigration(t, testDB, templateID, 1, 2)

	failed, err := currentTestAPI.getFailedDatabases(context.Background(), migrationID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(failed) != 0 {
		t.Errorf("expected 0 failed databases, got %d", len(failed))
	}
}

// =============================================================================
// BatchUpdateDatabaseVersions Tests
// Criteria B: Batch update edge cases
// =============================================================================

func TestBatchUpdateDatabaseVersions_Multiple(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	id1 := insertTestTenant(t, testDB, "database-1", templateID, 1)
	id2 := insertTestTenant(t, testDB, "database-2", templateID, 1)
	insertTestTenant(t, testDB, "database-3", templateID, 1) // Not updated

	err := currentTestAPI.batchUpdateDatabaseVersions(context.Background(), []int32{id1, id2}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify updates
	var v1, v2, v3 int
	testDB.QueryRow(`SELECT template_version FROM `+TableDatabases+` WHERE id = ?`, id1).Scan(&v1)
	testDB.QueryRow(`SELECT template_version FROM `+TableDatabases+` WHERE id = ?`, id2).Scan(&v2)
	testDB.QueryRow(`SELECT template_version FROM ` + TableDatabases + ` WHERE name = 'database-3'`).Scan(&v3)

	if v1 != 2 {
		t.Errorf("database-1 version = %d, want 2", v1)
	}
	if v2 != 2 {
		t.Errorf("database-2 version = %d, want 2", v2)
	}
	if v3 != 1 {
		t.Errorf("database-3 version = %d, want 1 (unchanged)", v3)
	}
}

func TestBatchUpdateDatabaseVersions_Empty(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	err := currentTestAPI.batchUpdateDatabaseVersions(context.Background(), []int32{}, 2)
	if err != nil {
		t.Errorf("empty batch should not error: %v", err)
	}
}

// =============================================================================
// RecordDatabaseMigration Tests
// Criteria B: failure recording and success clearing
// =============================================================================

func TestRecordDatabaseMigration_Success(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	migrationID := insertTestMigration(t, testDB, templateID, 1, 2)
	tenantID := insertTestTenant(t, testDB, "database-1", templateID, 1)

	err := currentTestAPI.recordDatabaseMigration(context.Background(), migrationID, tenantID, "success", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var count int
	testDB.QueryRow(`SELECT COUNT(*) FROM `+TableMigrationFailures+` WHERE database_id = ?`, tenantID).Scan(&count)
	if count != 0 {
		t.Errorf("expected no failure row for success, got %d", count)
	}
}

func TestRecordDatabaseMigration_Retry(t *testing.T) {
	testDB := setupTenantTestDB(t)
	defer testDB.Close()
	cleanup := setTestDB(t, testDB)
	defer cleanup()

	templateID := insertTestTemplate(t, testDB, "myapp", 2)
	migrationID := insertTestMigration(t, testDB, templateID, 1, 2)
	tenantID := insertTestTenant(t, testDB, "database-1", templateID, 1)

	// First attempt - failed
	err := currentTestAPI.recordDatabaseMigration(context.Background(), migrationID, tenantID, "failed", "connection error")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second attempt - success
	err = currentTestAPI.recordDatabaseMigration(context.Background(), migrationID, tenantID, "success", "")
	if err != nil {
		t.Fatalf("unexpected error on retry: %v", err)
	}

	var count int
	testDB.QueryRow(`SELECT COUNT(*) FROM `+TableMigrationFailures+` WHERE database_id = ?`, tenantID).Scan(&count)
	if count != 0 {
		t.Errorf("expected failure row to be cleared after success, got %d", count)
	}
}

// =============================================================================
// generateSchemaSQL Tests
// Criteria A: Core schema generation
// =============================================================================

func TestGenerateSchemaSQL_BasicTable(t *testing.T) {
	schema := Schema{Tables: []Table{
		{
			Name: "users",
			Pk:   []string{"id"},
			Columns: map[string]Col{
				"id":   {Name: "id", Type: "INTEGER"},
				"name": {Name: "name", Type: "TEXT"},
			},
		},
	}}

	sql := generateSchemaSQL(schema)

	if len(sql) < 2 {
		t.Fatalf("expected at least 2 statements, got %d", len(sql))
	}

	// First should be PRAGMA
	if sql[0] != "PRAGMA foreign_keys = ON" {
		t.Errorf("first statement = %s, want PRAGMA foreign_keys = ON", sql[0])
	}

	// Second should be CREATE TABLE
	if sql[1] == "" {
		t.Error("CREATE TABLE statement is empty")
	}
}

func TestGenerateSchemaSQL_WithIndexes(t *testing.T) {
	schema := Schema{Tables: []Table{
		{
			Name: "users",
			Pk:   []string{"id"},
			Columns: map[string]Col{
				"id":    {Name: "id", Type: "INTEGER"},
				"email": {Name: "email", Type: "TEXT"},
			},
			Indexes: []Index{
				{Name: "idx_email", Columns: []string{"email"}, Unique: true},
			},
		},
	}}

	sql := generateSchemaSQL(schema)

	// Should have PRAGMA, CREATE TABLE, and CREATE INDEX
	if len(sql) < 3 {
		t.Fatalf("expected at least 3 statements, got %d", len(sql))
	}

	// Find the index statement
	foundIndex := false
	for _, stmt := range sql {
		if stmt != "" && len(stmt) > 12 && stmt[:12] == "CREATE UNIQU" {
			foundIndex = true
			break
		}
	}
	if !foundIndex {
		t.Error("expected CREATE UNIQUE INDEX statement")
	}
}

func TestGenerateSchemaSQL_WithFTS(t *testing.T) {
	schema := Schema{Tables: []Table{
		{
			Name: "posts",
			Pk:   []string{"id"},
			Columns: map[string]Col{
				"id":      {Name: "id", Type: "INTEGER"},
				"title":   {Name: "title", Type: "TEXT"},
				"content": {Name: "content", Type: "TEXT"},
			},
			FTSColumns: []string{"title", "content"},
		},
	}}

	sql := generateSchemaSQL(schema)

	// Should include FTS setup statements
	foundFTS := false
	for _, stmt := range sql {
		if len(stmt) > 6 && stmt[:6] == "CREATE" && len(stmt) > 20 {
			if stmt[7:20] == "VIRTUAL TABLE" {
				foundFTS = true
				break
			}
		}
	}
	if !foundFTS {
		t.Error("expected FTS virtual table statement")
	}
}

func TestGenerateSchemaSQL_MultipleTables(t *testing.T) {
	schema := Schema{Tables: []Table{
		{
			Name:    "users",
			Pk:      []string{"id"},
			Columns: map[string]Col{"id": {Name: "id", Type: "INTEGER"}},
		},
		{
			Name:    "posts",
			Pk:      []string{"id"},
			Columns: map[string]Col{"id": {Name: "id", Type: "INTEGER"}},
		},
	}}

	sql := generateSchemaSQL(schema)

	// Count CREATE TABLE statements
	createCount := 0
	for _, stmt := range sql {
		if len(stmt) > 12 && stmt[:12] == "CREATE TABLE" {
			createCount++
		}
	}
	if createCount != 2 {
		t.Errorf("expected 2 CREATE TABLE statements, got %d", createCount)
	}
}
