package data

import (
	"errors"
	"testing"

	"github.com/atombasedev/atombase/tools"
)

func TestSchemaCacheSearchHelpers(t *testing.T) {
	schema := TablesToSchemaCache([]Table{testTableUsers, testTablePosts, testTableComments})

	t.Run("search fks found", func(t *testing.T) {
		fk, ok := schema.SearchFks("posts", "users")
		if !ok {
			t.Fatal("expected foreign key to be found")
		}
		if fk.From != "user_id" || fk.To != "id" {
			t.Fatalf("unexpected foreign key: %+v", fk)
		}
	})

	t.Run("search fks missing", func(t *testing.T) {
		if _, ok := schema.SearchFks("users", "posts"); ok {
			t.Fatal("expected no foreign key")
		}
	})

	t.Run("search table found", func(t *testing.T) {
		tbl, err := schema.SearchTbls("users")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tbl.Name != "users" {
			t.Fatalf("expected users table, got %q", tbl.Name)
		}
	})

	t.Run("search table missing", func(t *testing.T) {
		_, err := schema.SearchTbls("missing")
		if !errors.Is(err, tools.ErrTableNotFound) {
			t.Fatalf("expected ErrTableNotFound, got %v", err)
		}
	})

	t.Run("search column found", func(t *testing.T) {
		tbl := schema.Tables["users"]
		colType, err := tbl.SearchCols("name")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if colType != "TEXT" {
			t.Fatalf("expected TEXT, got %q", colType)
		}
	})

	t.Run("search column missing", func(t *testing.T) {
		tbl := schema.Tables["users"]
		_, err := tbl.SearchCols("email")
		if !errors.Is(err, tools.ErrColumnNotFound) {
			t.Fatalf("expected ErrColumnNotFound, got %v", err)
		}
	})
}

func TestBuildColumnTypeMap(t *testing.T) {
	schema := SchemaCache{
		Tables: map[string]CacheTable{
			"users": {
				Name: "users",
				Columns: map[string]string{
					"id":   "integer",
					"name": "text",
				},
			},
			"posts": {
				Name: "posts",
				Columns: map[string]string{
					"id":    "INTEGER",
					"title": "TEXT",
				},
			},
		},
	}

	types := schema.BuildColumnTypeMap()
	if types["id"] != "INTEGER" {
		t.Fatalf("expected INTEGER for id, got %q", types["id"])
	}
	if types["name"] != "TEXT" {
		t.Fatalf("expected TEXT for name, got %q", types["name"])
	}
	if types["title"] != "TEXT" {
		t.Fatalf("expected TEXT for title, got %q", types["title"])
	}
}
