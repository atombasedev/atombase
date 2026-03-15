package primarystore

import (
	"context"
	"database/sql"
	"testing"

	"github.com/atombasedev/atombase/definitions"
	_ "github.com/mattn/go-sqlite3"
)

const storeSchema = `
CREATE TABLE atombase_definitions (
	id INTEGER PRIMARY KEY,
	name TEXT UNIQUE NOT NULL,
	definition_type TEXT NOT NULL,
	current_version INTEGER DEFAULT 1
);
CREATE TABLE atombase_databases (
	id TEXT PRIMARY KEY NOT NULL,
	definition_id INTEGER NOT NULL,
	definition_version INTEGER DEFAULT 1,
	auth_token_encrypted BLOB,
	created_at TEXT,
	updated_at TEXT
);
CREATE TABLE atombase_users (
	id TEXT PRIMARY KEY NOT NULL,
	database_id TEXT UNIQUE
);
CREATE TABLE atombase_organizations (
	id TEXT PRIMARY KEY NOT NULL,
	database_id TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	owner_id TEXT NOT NULL
);
CREATE TABLE atombase_access_policies (
	definition_id INTEGER NOT NULL,
	version INTEGER NOT NULL,
	table_name TEXT NOT NULL,
	operation TEXT NOT NULL,
	conditions_json TEXT,
	PRIMARY KEY(definition_id, version, table_name, operation)
);
CREATE TABLE atombase_migrations (
	id INTEGER PRIMARY KEY,
	definition_id INTEGER NOT NULL,
	from_version INTEGER NOT NULL,
	to_version INTEGER NOT NULL,
	sql TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE atombase_migration_failures (
	database_id TEXT PRIMARY KEY,
	from_version INTEGER NOT NULL,
	to_version INTEGER NOT NULL,
	error TEXT,
	created_at TEXT NOT NULL
);
`

func setupStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(storeSchema); err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return store, db
}

func TestResolveDatabaseTarget(t *testing.T) {
	store, db := setupStore(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO atombase_definitions (id, name, definition_type, current_version) VALUES (1, 'market', 'global', 1), (2, 'notes', 'user', 1), (3, 'workspace', 'organization', 2)`)
	_, _ = db.Exec(`INSERT INTO atombase_databases (id, definition_id, definition_version) VALUES ('global-market', 1, 1), ('user-notes-db', 2, 1), ('org-db', 3, 2)`)
	_, _ = db.Exec(`INSERT INTO atombase_users (id, database_id) VALUES ('user-1', 'user-notes-db')`)
	_, _ = db.Exec(`INSERT INTO atombase_organizations (id, database_id, name, owner_id) VALUES ('org-1', 'org-db', 'Acme', 'user-1')`)

	global, err := store.ResolveDatabaseTarget(context.Background(), definitions.Principal{}, "global:global-market")
	if err != nil {
		t.Fatalf("resolve global failed: %v", err)
	}
	if global.DatabaseID != "global-market" {
		t.Fatalf("expected global-market, got %s", global.DatabaseID)
	}

	user, err := store.ResolveDatabaseTarget(context.Background(), definitions.Principal{UserID: "user-1"}, "user:notes")
	if err != nil {
		t.Fatalf("resolve user failed: %v", err)
	}
	if user.DatabaseID != "user-notes-db" {
		t.Fatalf("expected user-notes-db, got %s", user.DatabaseID)
	}

	org, err := store.ResolveDatabaseTarget(context.Background(), definitions.Principal{UserID: "user-1"}, "org:org-1")
	if err != nil {
		t.Fatalf("resolve org failed: %v", err)
	}
	if org.DatabaseID != "org-db" {
		t.Fatalf("expected org-db, got %s", org.DatabaseID)
	}
}

func TestLoadAccessPolicyAndMigrations(t *testing.T) {
	store, db := setupStore(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO atombase_access_policies (definition_id, version, table_name, operation, conditions_json) VALUES (3, 2, 'projects', 'select', '{"field":"auth.status","op":"eq","value":"member"}')`)
	_, _ = db.Exec(`INSERT INTO atombase_migrations (id, definition_id, from_version, to_version, sql, created_at) VALUES (1, 3, 1, 2, '["ALTER TABLE projects ADD COLUMN title"]', '2026-01-01T00:00:00Z')`)

	policy, err := store.LoadAccessPolicy(context.Background(), 3, 2, "projects", "select")
	if err != nil {
		t.Fatalf("LoadAccessPolicy failed: %v", err)
	}
	if policy == nil || policy.Condition == nil || policy.Condition.Field != "auth.status" {
		t.Fatalf("expected decoded policy condition, got %#v", policy)
	}

	migrations, err := store.GetMigrationsBetween(context.Background(), 3, 1, 2)
	if err != nil {
		t.Fatalf("GetMigrationsBetween failed: %v", err)
	}
	if len(migrations) != 1 || len(migrations[0].SQL) != 1 {
		t.Fatalf("expected one migration with one statement, got %#v", migrations)
	}
}
