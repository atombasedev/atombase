package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/atombasedev/atombase/definitions"
	"github.com/atombasedev/atombase/primarystore"
	_ "github.com/mattn/go-sqlite3"
)

const primaryPolicySchema = `
CREATE TABLE atombase_access_policies (
	definition_id INTEGER NOT NULL,
	version INTEGER NOT NULL,
	table_name TEXT NOT NULL,
	operation TEXT NOT NULL,
	conditions_json TEXT,
	PRIMARY KEY(definition_id, version, table_name, operation)
);
`

const tenantPolicySchema = `
CREATE TABLE atombase_membership (
	user_id TEXT NOT NULL PRIMARY KEY,
	role TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'active'
);
CREATE TABLE users (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL
);
CREATE TABLE posts (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users(id),
	author_id TEXT NOT NULL,
	title TEXT NOT NULL
);
`

func setupPolicyDAO(t *testing.T, principal definitions.Principal) (*TenantConnection, *sql.DB, *sql.DB) {
	t.Helper()
	primaryDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := primaryDB.Exec(primaryPolicySchema); err != nil {
		t.Fatal(err)
	}
	store, err := primarystore.New(primaryDB)
	if err != nil {
		t.Fatal(err)
	}

	tenantDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenantDB.Exec(tenantPolicySchema); err != nil {
		t.Fatal(err)
	}

	dao := &TenantConnection{
		Client:          tenantDB,
		Schema:          loadSchema(t, tenantDB),
		ID:              "org-db",
		DefinitionID:    1,
		DefinitionType:  definitions.DefinitionTypeOrganization,
		SchemaVersion:   1,
		DatabaseVersion: 1,
		Principal:       principal,
		primaryStore:    store,
	}
	return dao, primaryDB, tenantDB
}

func insertAccessPolicy(t *testing.T, db *sql.DB, table, operation, jsonCond string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO atombase_access_policies (definition_id, version, table_name, operation, conditions_json) VALUES (1, 1, ?, ?, ?)`, table, operation, jsonCond); err != nil {
		t.Fatal(err)
	}
}

func TestSelectJSON_NestedRelationPoliciesUseMembershipAndJoinedTableFilters(t *testing.T) {
	dao, primaryDB, tenantDB := setupPolicyDAO(t, definitions.Principal{
		UserID:     "user-1",
		AuthStatus: definitions.AuthStatusAuthenticated,
	})
	defer primaryDB.Close()
	defer tenantDB.Close()

	insertAccessPolicy(t, primaryDB, "users", "select", `{"field":"auth.status","op":"eq","value":"member"}`)
	insertAccessPolicy(t, primaryDB, "posts", "select", `{"field":"old.author_id","op":"eq","value":"auth.id"}`)

	if _, err := tenantDB.Exec(`INSERT INTO atombase_membership (user_id, role) VALUES ('user-1', 'member')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tenantDB.Exec(`INSERT INTO users (id, name) VALUES (1, 'Alice')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tenantDB.Exec(`INSERT INTO posts (id, user_id, author_id, title) VALUES (1, 1, 'user-1', 'mine'), (2, 1, 'user-2', 'theirs')`); err != nil {
		t.Fatal(err)
	}

	result, err := dao.SelectJSON(context.Background(), "users", SelectQuery{
		Select: []any{
			"id",
			"name",
			map[string]any{"posts": []any{"id", "title"}},
		},
	}, false)
	if err != nil {
		t.Fatalf("SelectJSON failed: %v", err)
	}

	var payload []map[string]any
	if err := json.Unmarshal(result.Data, &payload); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected 1 user row, got %d", len(payload))
	}
	posts, ok := payload[0]["posts"].([]any)
	if !ok {
		t.Fatalf("expected nested posts array, got %#v", payload[0]["posts"])
	}
	if len(posts) != 1 {
		t.Fatalf("expected exactly 1 authorized nested post, got %d", len(posts))
	}
}

func TestUpdateJSON_OrganizationPolicyFiltersRowsInSQL(t *testing.T) {
	dao, primaryDB, tenantDB := setupPolicyDAO(t, definitions.Principal{
		UserID:     "user-1",
		AuthStatus: definitions.AuthStatusAuthenticated,
	})
	defer primaryDB.Close()
	defer tenantDB.Close()

	insertAccessPolicy(t, primaryDB, "posts", "update", `{"and":[{"field":"auth.status","op":"eq","value":"member"},{"field":"old.author_id","op":"eq","value":"auth.id"}]}`)

	if _, err := tenantDB.Exec(`INSERT INTO atombase_membership (user_id, role) VALUES ('user-1', 'member')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tenantDB.Exec(`INSERT INTO users (id, name) VALUES (1, 'Alice')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tenantDB.Exec(`INSERT INTO posts (id, user_id, author_id, title) VALUES (1, 1, 'user-2', 'locked'), (2, 1, 'user-1', 'editable')`); err != nil {
		t.Fatal(err)
	}

	resp, err := dao.UpdateJSON(context.Background(), "posts", UpdateRequest{
		Data: map[string]any{"title": "updated"},
		Where: []map[string]any{
			{"user_id": map[string]any{"eq": 1}},
		},
	})
	if err != nil {
		t.Fatalf("UpdateJSON failed: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(resp, &body); err != nil {
		t.Fatal(err)
	}
	if body["rows_affected"].(float64) != 1 {
		t.Fatalf("expected only authorized row to update, got %#v", body)
	}

	var lockedTitle, editableTitle string
	if err := tenantDB.QueryRow(`SELECT title FROM posts WHERE id = 1`).Scan(&lockedTitle); err != nil {
		t.Fatal(err)
	}
	if err := tenantDB.QueryRow(`SELECT title FROM posts WHERE id = 2`).Scan(&editableTitle); err != nil {
		t.Fatal(err)
	}
	if lockedTitle != "locked" || editableTitle != "updated" {
		t.Fatalf("unexpected titles after update: locked=%q editable=%q", lockedTitle, editableTitle)
	}
}
