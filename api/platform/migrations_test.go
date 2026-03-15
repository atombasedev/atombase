package platform

import (
	"context"
	"strings"
	"testing"
)

func TestApplyMerges_TableAndColumnRename(t *testing.T) {
	changes := []SchemaDiff{
		{Type: "drop_table", Table: "old_posts"},
		{Type: "add_table", Table: "posts"},
		{Type: "drop_column", Table: "posts", Column: "title"},
		{Type: "add_column", Table: "posts", Column: "headline"},
	}

	renames := applyMerges(changes, []Merge{{Old: 0, New: 1}, {Old: 2, New: 3}})
	if len(renames) != 2 {
		t.Fatalf("expected 2 renames, got %d", len(renames))
	}
	if renames[0].Type != "rename_table" || renames[0].OldName != "old_posts" || renames[0].NewName != "posts" {
		t.Fatalf("unexpected table rename: %+v", renames[0])
	}
	if renames[1].Type != "rename_column" || renames[1].Table != "posts" || renames[1].OldName != "title" || renames[1].NewName != "headline" {
		t.Fatalf("unexpected column rename: %+v", renames[1])
	}
}

func TestRequiresMirrorTable(t *testing.T) {
	tests := []struct {
		name     string
		old      Col
		new      Col
		required bool
	}{
		{name: "type_only", old: Col{Name: "age", Type: "INTEGER"}, new: Col{Name: "age", Type: "TEXT"}, required: false},
		{name: "add_fk", old: Col{Name: "user_id", Type: "INTEGER"}, new: Col{Name: "user_id", Type: "INTEGER", References: "users.id"}, required: true},
		{name: "check_change", old: Col{Name: "age", Type: "INTEGER"}, new: Col{Name: "age", Type: "INTEGER", Check: "age >= 0"}, required: true},
		{name: "generated_change", old: Col{Name: "full_name", Type: "TEXT"}, new: Col{Name: "full_name", Type: "TEXT", Generated: &Generated{Expr: "first || last"}}, required: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiresMirrorTable(tt.old, tt.new); got != tt.required {
				t.Fatalf("requiresMirrorTable() = %v, want %v", got, tt.required)
			}
		})
	}
}

func TestGenerateCreateTableSQL_WithFTSAndConstraints(t *testing.T) {
	table := Table{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":      {Name: "id", Type: "INTEGER"},
			"title":   {Name: "title", Type: "TEXT", NotNull: true},
			"author":  {Name: "author", Type: "TEXT", Default: "system"},
			"ownerId": {Name: "ownerId", Type: "TEXT", References: "users.id", OnDelete: "CASCADE"},
		},
	}

	sql := generateCreateTableSQL(table)
	if !strings.Contains(sql, "CREATE TABLE [posts]") {
		t.Fatalf("missing create table: %s", sql)
	}
	if !strings.Contains(sql, "[id] INTEGER PRIMARY KEY") {
		t.Fatalf("missing integer primary key: %s", sql)
	}
	if !strings.Contains(sql, "NOT NULL") || !strings.Contains(sql, "DEFAULT 'system'") {
		t.Fatalf("missing constraints/defaults: %s", sql)
	}
	if !strings.Contains(sql, "FOREIGN KEY ([ownerId]) REFERENCES [users]([id]) ON DELETE CASCADE") {
		t.Fatalf("missing fk constraint: %s", sql)
	}
}

func TestGenerateAddColumnSQL_NotNullAutoFix(t *testing.T) {
	sql := generateAddColumnSQL("posts", Col{Name: "count", Type: "INTEGER", NotNull: true})
	if !strings.Contains(sql, "NOT NULL DEFAULT 0") {
		t.Fatalf("expected not null default autofix, got %s", sql)
	}
}

func TestGenerateMirrorTableSQL_RebuildsIndexesAndFTS(t *testing.T) {
	oldTable := Table{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"title": {Name: "title", Type: "TEXT"},
		},
	}
	newTable := Table{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"title": {Name: "title", Type: "TEXT", Check: "length(title) > 0"},
		},
		Indexes:    []Index{{Name: "idx_posts_title", Columns: []string{"title"}}},
		FTSColumns: []string{"title"},
	}

	statements := generateMirrorTableSQL(oldTable, newTable)
	if len(statements) < 6 {
		t.Fatalf("expected mirror table rebuild with index and fts, got %d statements", len(statements))
	}
	if !strings.Contains(statements[0], "CREATE TABLE [posts_new]") {
		t.Fatalf("missing temp table create: %s", statements[0])
	}
	if !strings.Contains(statements[4], "CREATE INDEX IF NOT EXISTS [idx_posts_title]") {
		t.Fatalf("missing recreated index: %s", statements[4])
	}
	if !strings.Contains(strings.Join(statements, "\n"), "CREATE VIRTUAL TABLE IF NOT EXISTS [posts_fts]") {
		t.Fatalf("missing recreated fts statements: %#v", statements)
	}
}

func TestGenerateMigrationPlan_RenameAndFTSOrder(t *testing.T) {
	oldSchema := Schema{Tables: []Table{{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":    {Name: "id", Type: "INTEGER"},
			"title": {Name: "title", Type: "TEXT"},
		},
	}}}
	newSchema := Schema{Tables: []Table{{
		Name: "posts",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":       {Name: "id", Type: "INTEGER"},
			"headline": {Name: "headline", Type: "TEXT"},
		},
		FTSColumns: []string{"headline"},
	}}}

	changes := diffSchemas(oldSchema, newSchema)
	plan, err := GenerateMigrationPlan(oldSchema, newSchema, changes, []Merge{{Old: 0, New: 1}})
	if err != nil {
		t.Fatalf("GenerateMigrationPlan failed: %v", err)
	}
	if len(plan.SQL) < 2 {
		t.Fatalf("expected rename and fts statements, got %#v", plan.SQL)
	}
	if plan.SQL[0] != "ALTER TABLE [posts] RENAME COLUMN [title] TO [headline]" {
		t.Fatalf("expected rename first, got %s", plan.SQL[0])
	}
	if !strings.Contains(strings.Join(plan.SQL, "\n"), "CREATE VIRTUAL TABLE IF NOT EXISTS [posts_fts]") {
		t.Fatalf("missing fts sql: %#v", plan.SQL)
	}
}

func TestCreateMigration_PersistsDefinitionID(t *testing.T) {
	api, db := setupPlatformAPI(t)
	defer db.Close()

	migration, err := api.createMigration(context.Background(), 7, 1, 2, []string{"ALTER TABLE [posts] ADD COLUMN [title]"})
	if err != nil {
		t.Fatalf("createMigration failed: %v", err)
	}
	if migration.DefinitionID != 7 {
		t.Fatalf("definition id = %d, want 7", migration.DefinitionID)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM atombase_migrations WHERE definition_id = 7 AND from_version = 1 AND to_version = 2`).Scan(&count); err != nil {
		t.Fatalf("failed to query migration row: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected migration row to be inserted, got %d", count)
	}
}
