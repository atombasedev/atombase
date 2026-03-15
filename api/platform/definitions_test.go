package platform

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/atombasedev/atombase/primarystore"
	"github.com/atombasedev/atombase/tools"
	_ "github.com/mattn/go-sqlite3"
)

const platformSchema = `
CREATE TABLE atombase_definitions (
	id INTEGER PRIMARY KEY,
	name TEXT UNIQUE NOT NULL,
	definition_type TEXT NOT NULL,
	roles_json TEXT,
	current_version INTEGER DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE atombase_definitions_history (
	id INTEGER PRIMARY KEY,
	definition_id INTEGER NOT NULL,
	version INTEGER NOT NULL,
	schema_json TEXT NOT NULL,
	checksum TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(definition_id, version)
);
CREATE TABLE atombase_access_policies (
	definition_id INTEGER NOT NULL,
	version INTEGER NOT NULL,
	table_name TEXT NOT NULL,
	operation TEXT NOT NULL,
	conditions_json TEXT,
	PRIMARY KEY(definition_id, version, table_name, operation)
);
CREATE TABLE atombase_management_policies (
	definition_id INTEGER NOT NULL,
	role TEXT NOT NULL,
	action TEXT NOT NULL,
	target_roles_json TEXT,
	PRIMARY KEY(definition_id, role, action)
);
CREATE TABLE atombase_migrations (
	id INTEGER PRIMARY KEY,
	definition_id INTEGER NOT NULL,
	from_version INTEGER NOT NULL,
	to_version INTEGER NOT NULL,
	sql TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE atombase_databases (
	id TEXT PRIMARY KEY NOT NULL,
	definition_id INTEGER NOT NULL,
	definition_version INTEGER DEFAULT 1,
	auth_token_encrypted BLOB,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE atombase_users (
	id TEXT PRIMARY KEY NOT NULL,
	database_id TEXT UNIQUE,
	updated_at TEXT
);
CREATE TABLE atombase_organizations (
	id TEXT PRIMARY KEY NOT NULL,
	database_id TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	owner_id TEXT NOT NULL,
	max_members INTEGER,
	metadata TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
`

func setupPlatformAPI(t *testing.T) (*API, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(platformSchema); err != nil {
		t.Fatal(err)
	}
	store, err := primarystore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewAPI(store)
	if err != nil {
		t.Fatal(err)
	}
	return api, db
}

func TestDefinitionCRUDAndHistory(t *testing.T) {
	api, db := setupPlatformAPI(t)
	defer db.Close()

	schema := Schema{Tables: []Table{{Name: "posts", Pk: []string{"id"}, Columns: map[string]Col{
		"id":        {Name: "id", Type: "INTEGER"},
		"author_id": {Name: "author_id", Type: "TEXT"},
	}}}}

	created, err := api.createDefinition(context.Background(), CreateDefinitionRequest{
		Name:   "posts",
		Type:   "organization",
		Roles:  []string{"owner", "member"},
		Schema: schema,
		Access: map[string]OperationPolicy{
			"posts": {
				Select: &Condition{Field: "auth.status", Op: "eq", Value: "member"},
				Update: &Condition{Field: "auth.role", Op: "eq", Value: "owner"},
			},
		},
	})
	if err != nil {
		t.Fatalf("createDefinition failed: %v", err)
	}
	if created.CurrentVersion != 1 {
		t.Fatalf("expected version 1, got %d", created.CurrentVersion)
	}

	history, err := api.getDefinitionHistory(context.Background(), "posts")
	if err != nil {
		t.Fatalf("getDefinitionHistory failed: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history row, got %d", len(history))
	}

	nextSchema := Schema{Tables: []Table{
		{Name: "posts", Pk: []string{"id"}, Columns: map[string]Col{
			"id":        {Name: "id", Type: "INTEGER"},
			"author_id": {Name: "author_id", Type: "TEXT"},
			"title":     {Name: "title", Type: "TEXT"},
		}},
	}}
	version, err := api.pushDefinition(context.Background(), "posts", PushDefinitionRequest{
		Schema: nextSchema,
		Access: map[string]OperationPolicy{
			"posts": {
				Select: &Condition{Field: "auth.status", Op: "eq", Value: "member"},
			},
		},
	})
	if err != nil {
		t.Fatalf("pushDefinition failed: %v", err)
	}
	if version.Version != 2 {
		t.Fatalf("expected version 2, got %d", version.Version)
	}

	var migrationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM atombase_migrations WHERE definition_id = ? AND from_version = 1 AND to_version = 2`, created.ID).Scan(&migrationCount); err != nil {
		t.Fatalf("failed to query migration row: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected 1 migration row, got %d", migrationCount)
	}
}

func TestPushDefinition_RejectsNoChangesAndInvalidFKs(t *testing.T) {
	api, db := setupPlatformAPI(t)
	defer db.Close()

	schema := Schema{Tables: []Table{{Name: "posts", Pk: []string{"id"}, Columns: map[string]Col{
		"id": {Name: "id", Type: "INTEGER"},
	}}}}

	_, err := api.createDefinition(context.Background(), CreateDefinitionRequest{
		Name:   "posts",
		Type:   "global",
		Schema: schema,
		Access: map[string]OperationPolicy{"posts": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "anonymous"}}},
	})
	if err != nil {
		t.Fatalf("createDefinition failed: %v", err)
	}

	if _, err := api.pushDefinition(context.Background(), "posts", PushDefinitionRequest{
		Schema: schema,
		Access: map[string]OperationPolicy{"posts": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "anonymous"}}},
	}); err != tools.ErrNoChanges {
		t.Fatalf("expected ErrNoChanges, got %v", err)
	}

	invalidSchema := Schema{Tables: []Table{{Name: "posts", Pk: []string{"id"}, Columns: map[string]Col{
		"id":      {Name: "id", Type: "INTEGER"},
		"user_id": {Name: "user_id", Type: "INTEGER", References: "users.id"},
	}}}}
	if _, err := api.pushDefinition(context.Background(), "posts", PushDefinitionRequest{
		Schema: invalidSchema,
		Access: map[string]OperationPolicy{"posts": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "anonymous"}}},
	}); err == nil {
		t.Fatal("expected invalid migration error for missing FK table")
	}
}

func TestCreateDatabase_AttachesUserAndOrgMetadata(t *testing.T) {
	api, db := setupPlatformAPI(t)
	defer db.Close()

	_, _ = db.Exec(`INSERT INTO atombase_users (id) VALUES ('user-1')`)
	_, err := api.createDefinition(context.Background(), CreateDefinitionRequest{
		Name: "notes",
		Type: "user",
		Schema: Schema{Tables: []Table{{Name: "notes", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}}}},
		Access: map[string]OperationPolicy{"notes": {Select: &Condition{Field: "auth.id", Op: "eq", Value: "auth.id"}}},
	})
	if err != nil {
		t.Fatalf("createDefinition(user) failed: %v", err)
	}
	_, err = api.createDefinition(context.Background(), CreateDefinitionRequest{
		Name: "workspace",
		Type: "organization",
		Schema: Schema{Tables: []Table{{Name: "projects", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}}}},
		Access: map[string]OperationPolicy{"projects": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "member"}}},
	})
	if err != nil {
		t.Fatalf("createDefinition(org) failed: %v", err)
	}

	oldCreate := tursoCreateDatabaseFn
	oldDelete := tursoDeleteDatabaseFn
	oldToken := tursoCreateTokenFn
	oldBatch := batchExecuteWithTokenFn
	defer func() {
		tursoCreateDatabaseFn = oldCreate
		tursoDeleteDatabaseFn = oldDelete
		tursoCreateTokenFn = oldToken
		batchExecuteWithTokenFn = oldBatch
	}()
	tursoCreateDatabaseFn = func(ctx context.Context, name string) error { return nil }
	tursoDeleteDatabaseFn = func(ctx context.Context, name string) error { return nil }
	tursoCreateTokenFn = func(ctx context.Context, name string) (string, error) { return "token", nil }
	var executed map[string][]string
	batchExecuteWithTokenFn = func(ctx context.Context, dbName, token string, statements []string) error {
		if executed == nil {
			executed = map[string][]string{}
		}
		executed[dbName] = append([]string{}, statements...)
		return nil
	}

	userDB, err := api.createDatabase(context.Background(), CreateDatabaseRequest{
		ID:         "notes-db",
		Definition: "notes",
		UserID:     "user-1",
	})
	if err != nil {
		t.Fatalf("createDatabase(user) failed: %v", err)
	}
	if userDB.DefinitionType != "user" {
		t.Fatalf("expected user definition type, got %s", userDB.DefinitionType)
	}

	orgDB, err := api.createDatabase(context.Background(), CreateDatabaseRequest{
		ID:               "workspace-db",
		Definition:       "workspace",
		OrganizationID:   "org-1",
		OrganizationName: "Acme",
		OwnerID:          "user-1",
	})
	if err != nil {
		t.Fatalf("createDatabase(org) failed: %v", err)
	}
	if orgDB.OrganizationID != "org-1" {
		t.Fatalf("expected org id org-1, got %s", orgDB.OrganizationID)
	}
	foundOwnerSeed := false
	for _, stmt := range executed["workspace-db"] {
		if strings.Contains(stmt, "INSERT OR IGNORE INTO atombase_membership") && strings.Contains(stmt, "'user-1'") && strings.Contains(stmt, "'owner'") {
			foundOwnerSeed = true
			break
		}
	}
	if !foundOwnerSeed {
		t.Fatal("expected org database initialization to seed owner membership")
	}
}

func TestCreateDefinition_PersistsManagementPolicies(t *testing.T) {
	api, db := setupPlatformAPI(t)
	defer db.Close()

	created, err := api.createDefinition(context.Background(), CreateDefinitionRequest{
		Name:  "workspace",
		Type:  "organization",
		Roles: []string{"owner", "admin", "member", "viewer"},
		Management: ManagementMap{
			"owner": {
				Invite:            ManagementPermission{Allowed: true, Any: true},
				AssignRole:        ManagementPermission{Allowed: true, Any: true},
				RemoveMember:      ManagementPermission{Allowed: true, Any: true},
				UpdateOrg:         true,
				DeleteOrg:         true,
				TransferOwnership: true,
			},
			"admin": {
				Invite:       ManagementPermission{Allowed: true, Roles: []string{"member", "viewer"}},
				AssignRole:   ManagementPermission{Allowed: true, Roles: []string{"member", "viewer"}},
				RemoveMember: ManagementPermission{Allowed: true, Roles: []string{"member", "viewer"}},
			},
		},
		Schema: Schema{Tables: []Table{{Name: "projects", Pk: []string{"id"}, Columns: map[string]Col{
			"id": {Name: "id", Type: "INTEGER"},
		}}}},
		Access: map[string]OperationPolicy{"projects": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "member"}}},
	})
	if err != nil {
		t.Fatalf("createDefinition(org with management) failed: %v", err)
	}

	if !created.Management["owner"].Invite.Any {
		t.Fatal("expected owner invite management to round-trip")
	}
	if got := created.Management["admin"].AssignRole.Roles; len(got) != 2 || got[0] != "member" || got[1] != "viewer" {
		t.Fatalf("unexpected admin assignRole targets: %#v", got)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM atombase_management_policies WHERE definition_id = ?`, created.ID).Scan(&count); err != nil {
		t.Fatalf("count management policies: %v", err)
	}
	if count != 9 {
		t.Fatalf("expected 9 management policy rows, got %d", count)
	}
}

func TestPushDefinition_UsesMergeAndProbesExistingDatabase(t *testing.T) {
	api, db := setupPlatformAPI(t)
	defer db.Close()

	initial := Schema{Tables: []Table{{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"title": {Name: "title", Type: "TEXT"},
		},
	}}}

	created, err := api.createDefinition(context.Background(), CreateDefinitionRequest{
		Name:   "posts",
		Type:   "organization",
		Roles:  []string{"owner", "member"},
		Schema: initial,
		Access: map[string]OperationPolicy{
			"posts": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "member"}},
		},
	})
	if err != nil {
		t.Fatalf("createDefinition failed: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO atombase_databases (id, definition_id, definition_version, auth_token_encrypted, created_at, updated_at)
		VALUES ('org-db', ?, 1, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`, created.ID, []byte("probe-token"))
	if err != nil {
		t.Fatalf("failed to insert database row: %v", err)
	}

	oldBatch := batchExecuteWithTokenFn
	defer func() {
		batchExecuteWithTokenFn = oldBatch
	}()

	var probedDB string
	var probeSQL []string
	batchExecuteWithTokenFn = func(ctx context.Context, dbName, token string, statements []string) error {
		probedDB = dbName
		probeSQL = append([]string(nil), statements...)
		return nil
	}

	next := Schema{Tables: []Table{{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":       {Name: "id", Type: "INTEGER"},
			"headline": {Name: "headline", Type: "TEXT"},
		},
	}}}

	version, err := api.pushDefinition(context.Background(), "posts", PushDefinitionRequest{
		Schema: next,
		Access: map[string]OperationPolicy{
			"posts": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "member"}},
		},
		Merge: []Merge{{Old: 0, New: 1}},
	})
	if err != nil {
		t.Fatalf("pushDefinition failed: %v", err)
	}
	if version.Version != 2 {
		t.Fatalf("expected version 2, got %d", version.Version)
	}
	if probedDB != "org-db" {
		t.Fatalf("expected probe against org-db, got %q", probedDB)
	}
	if len(probeSQL) != 1 || probeSQL[0] != "ALTER TABLE [posts] RENAME COLUMN [title] TO [headline]" {
		t.Fatalf("unexpected probe sql: %#v", probeSQL)
	}

	var databaseVersion int
	if err := db.QueryRow(`SELECT definition_version FROM atombase_databases WHERE id = 'org-db'`).Scan(&databaseVersion); err != nil {
		t.Fatalf("failed to query database version: %v", err)
	}
	if databaseVersion != 2 {
		t.Fatalf("expected probed database version 2, got %d", databaseVersion)
	}
}

func TestPushDefinition_LocalProbeFailureStopsRemoteProbe(t *testing.T) {
	api, db := setupPlatformAPI(t)
	defer db.Close()

	initial := Schema{Tables: []Table{{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"title": {Name: "title", Type: "TEXT"},
		},
	}}}

	created, err := api.createDefinition(context.Background(), CreateDefinitionRequest{
		Name:   "posts",
		Type:   "organization",
		Roles:  []string{"owner", "member"},
		Schema: initial,
		Access: map[string]OperationPolicy{
			"posts": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "member"}},
		},
	})
	if err != nil {
		t.Fatalf("createDefinition failed: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO atombase_databases (id, definition_id, definition_version, auth_token_encrypted, created_at, updated_at)
		VALUES ('org-db', ?, 1, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`, created.ID, []byte("probe-token"))
	if err != nil {
		t.Fatalf("failed to insert database row: %v", err)
	}

	oldBatch := batchExecuteWithTokenFn
	defer func() {
		batchExecuteWithTokenFn = oldBatch
	}()

	remoteCalled := false
	batchExecuteWithTokenFn = func(ctx context.Context, dbName, token string, statements []string) error {
		remoteCalled = true
		return nil
	}

	next := Schema{Tables: []Table{{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":     {Name: "id", Type: "INTEGER"},
			"broken": {Name: "broken", Type: "TEXT", Default: map[string]any{"sql": "("}},
		},
	}}}

	_, err = api.pushDefinition(context.Background(), "posts", PushDefinitionRequest{
		Schema: next,
		Access: map[string]OperationPolicy{
			"posts": {Select: &Condition{Field: "auth.status", Op: "eq", Value: "member"}},
		},
	})
	if err == nil {
		t.Fatal("expected pushDefinition to fail on local probe")
	}
	if !strings.Contains(err.Error(), "local migration probe failed") {
		t.Fatalf("expected local probe invalid migration error, got %v", err)
	}
	if remoteCalled {
		t.Fatal("expected local probe failure to stop remote probe")
	}
}
