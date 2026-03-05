package primarystore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atombasedev/atombase/tools"
	_ "github.com/mattn/go-sqlite3"
)

const (
	createDatabasesTable = `
		CREATE TABLE atombase_databases (
			id INTEGER PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			template_id INTEGER,
			template_version INTEGER,
			auth_token_encrypted BLOB,
			updated_at TEXT NOT NULL
		)
	`

	createMigrationsTable = `
		CREATE TABLE atombase_migrations (
			id INTEGER PRIMARY KEY,
			template_id INTEGER NOT NULL,
			from_version INTEGER NOT NULL,
			to_version INTEGER NOT NULL,
			sql TEXT NOT NULL,
			created_at TEXT NOT NULL
		)
	`

	createMigrationFailuresTable = `
		CREATE TABLE atombase_migration_failures (
			database_id INTEGER PRIMARY KEY,
			from_version INTEGER NOT NULL,
			to_version INTEGER NOT NULL,
			error TEXT,
			created_at TEXT NOT NULL
		)
	`
)

func setupTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	for _, stmt := range []string{createDatabasesTable, createMigrationsTable, createMigrationFailuresTable} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			t.Fatalf("create schema: %v", err)
		}
	}

	store, err := New(db)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create store: %v", err)
	}

	t.Cleanup(func() {
		_ = store.Close()
		_ = db.Close()
	})

	return store, db
}

func insertDatabaseRow(t *testing.T, db *sql.DB, name string, templateID any, version any) int32 {
	t.Helper()

	res, err := db.Exec(
		`INSERT INTO atombase_databases (name, template_id, template_version, updated_at) VALUES (?, ?, ?, ?)`,
		name,
		templateID,
		version,
		"2026-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("insert database row: %v", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("read inserted id: %v", err)
	}

	return int32(id)
}

func insertMigrationRow(t *testing.T, db *sql.DB, templateID int32, fromVersion, toVersion int, sqlJSON string) int64 {
	t.Helper()

	res, err := db.Exec(
		`INSERT INTO atombase_migrations (template_id, from_version, to_version, sql, created_at) VALUES (?, ?, ?, ?, ?)`,
		templateID,
		fromVersion,
		toVersion,
		sqlJSON,
		"2026-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("insert migration row: %v", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("read migration id: %v", err)
	}

	return id
}

func TestNew_NilConnection(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error for nil connection")
	}
}

func TestLookupDatabaseByName(t *testing.T) {
	store, db := setupTestStore(t)

	id := insertDatabaseRow(t, db, "tenant-alpha", nil, nil)

	meta, err := store.LookupDatabaseByName("tenant-alpha")
	if err != nil {
		t.Fatalf("lookup database: %v", err)
	}

	if meta.ID != id {
		t.Fatalf("expected id %d, got %d", id, meta.ID)
	}

	if meta.TemplateID != 0 {
		t.Fatalf("expected default template id 0, got %d", meta.TemplateID)
	}

	if meta.DatabaseVersion != 1 {
		t.Fatalf("expected default template version 1, got %d", meta.DatabaseVersion)
	}

	_, err = store.LookupDatabaseByName("does-not-exist")
	if !errors.Is(err, tools.ErrDatabaseNotFound) {
		t.Fatalf("expected ErrDatabaseNotFound, got %v", err)
	}
}

func TestGetMigrationsBetween(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(t *testing.T, db *sql.DB)
		fromVersion   int
		toVersion     int
		assertSuccess func(t *testing.T, got []TemplateMigration)
		errContains   string
	}{
		{
			name: "contiguous range is returned in order",
			setup: func(t *testing.T, db *sql.DB) {
				insertMigrationRow(t, db, 42, 1, 2, `["ALTER TABLE users ADD COLUMN email TEXT"]`)
				insertMigrationRow(t, db, 42, 2, 3, `["CREATE INDEX users_email_idx ON users(email)"]`)
			},
			fromVersion: 1,
			toVersion:   3,
			assertSuccess: func(t *testing.T, got []TemplateMigration) {
				t.Helper()

				if len(got) != 2 {
					t.Fatalf("expected 2 migrations, got %d", len(got))
				}

				if got[0].FromVersion != 1 || got[0].ToVersion != 2 {
					t.Fatalf("unexpected first step: %+v", got[0])
				}

				if got[1].FromVersion != 2 || got[1].ToVersion != 3 {
					t.Fatalf("unexpected second step: %+v", got[1])
				}

				if len(got[0].SQL) != 1 || got[0].SQL[0] != "ALTER TABLE users ADD COLUMN email TEXT" {
					t.Fatalf("unexpected sql payload for migration 1->2: %v", got[0].SQL)
				}
			},
		},
		{
			name: "missing first step is detected",
			setup: func(t *testing.T, db *sql.DB) {
				insertMigrationRow(t, db, 42, 2, 3, `["ALTER TABLE users ADD COLUMN email TEXT"]`)
			},
			fromVersion: 1,
			toVersion:   3,
			errContains: "missing migration step from version 1",
		},
		{
			name: "missing terminal step is detected",
			setup: func(t *testing.T, db *sql.DB) {
				insertMigrationRow(t, db, 42, 1, 2, `["ALTER TABLE users ADD COLUMN email TEXT"]`)
			},
			fromVersion: 1,
			toVersion:   3,
			errContains: "missing migrations to reach version 3",
		},
		{
			name: "invalid sql payload fails with migration id",
			setup: func(t *testing.T, db *sql.DB) {
				insertMigrationRow(t, db, 42, 1, 2, `not-json`)
			},
			fromVersion: 1,
			toVersion:   2,
			errContains: "failed to decode migration",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, db := setupTestStore(t)
			tc.setup(t, db)

			migrations, err := store.GetMigrationsBetween(context.Background(), 42, tc.fromVersion, tc.toVersion)

			if tc.errContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tc.errContains)
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error containing %q, got %v", tc.errContains, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.assertSuccess != nil {
				tc.assertSuccess(t, migrations)
			}
		})
	}
}

func TestUpdateDatabaseVersion(t *testing.T) {
	store, db := setupTestStore(t)
	id := insertDatabaseRow(t, db, "tenant-version-test", 7, 1)

	err := store.UpdateDatabaseVersion(context.Background(), id, 4)
	if err != nil {
		t.Fatalf("update database version: %v", err)
	}

	var version int
	var updatedAt string
	err = db.QueryRow(`SELECT template_version, updated_at FROM atombase_databases WHERE id = ?`, id).Scan(&version, &updatedAt)
	if err != nil {
		t.Fatalf("query updated database: %v", err)
	}

	if version != 4 {
		t.Fatalf("expected version 4, got %d", version)
	}

	if _, err := time.Parse(time.RFC3339, updatedAt); err != nil {
		t.Fatalf("expected RFC3339 timestamp, got %q (%v)", updatedAt, err)
	}
}

func TestRecordMigrationFailure_UpsertAndNilErrorNoop(t *testing.T) {
	store, db := setupTestStore(t)
	databaseID := insertDatabaseRow(t, db, "tenant-failure-test", 5, 1)

	_, err := db.Exec(
		`INSERT INTO atombase_migration_failures (database_id, from_version, to_version, error, created_at) VALUES (?, 1, 2, 'old failure', ?)`,
		databaseID,
		"2026-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("seed migration failure: %v", err)
	}

	store.RecordMigrationFailure(context.Background(), databaseID, 2, 3, errors.New("new failure"))

	var fromVersion int
	var toVersion int
	var errMsg string
	err = db.QueryRow(`SELECT from_version, to_version, error FROM atombase_migration_failures WHERE database_id = ?`, databaseID).Scan(&fromVersion, &toVersion, &errMsg)
	if err != nil {
		t.Fatalf("read migration failure after upsert: %v", err)
	}

	if fromVersion != 2 || toVersion != 3 || errMsg != "new failure" {
		t.Fatalf("unexpected upserted failure row: from=%d to=%d err=%q", fromVersion, toVersion, errMsg)
	}

	store.RecordMigrationFailure(context.Background(), databaseID, 99, 100, nil)

	err = db.QueryRow(`SELECT from_version, to_version, error FROM atombase_migration_failures WHERE database_id = ?`, databaseID).Scan(&fromVersion, &toVersion, &errMsg)
	if err != nil {
		t.Fatalf("read migration failure after nil-error no-op: %v", err)
	}

	if fromVersion != 2 || toVersion != 3 || errMsg != "new failure" {
		t.Fatalf("expected no-op when migrationErr=nil, got from=%d to=%d err=%q", fromVersion, toVersion, errMsg)
	}
}
