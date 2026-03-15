package data

import (
	"strings"
	"testing"

	"github.com/atombasedev/atombase/config"
)

func testQuerySchema() SchemaCache {
	unrelatedTable := Table{
		Name: "tags",
		Pk:   []string{"id"},
		Columns: map[string]Col{
			"id":   {Name: "id", Type: "INTEGER", NotNull: true},
			"name": {Name: "name", Type: "TEXT", NotNull: true},
		},
	}
	return TablesToSchemaCache([]Table{testTableUsers, testTablePosts, testTableComments, unrelatedTable})
}

func TestParseCustomJoinQuery(t *testing.T) {
	schema := testQuerySchema()

	tests := []struct {
		name    string
		query   SelectQuery
		wantErr string
		check   func(t *testing.T, cjq *CustomJoinQuery)
	}{
		{
			name: "valid join with base and joined columns",
			query: SelectQuery{
				Select: []any{"id", "posts.title", map[string]any{"post_id": "posts.id"}},
				Join: []JoinClause{
					{
						Table: "posts",
						On: []map[string]any{
							{"users.id": map[string]any{"eq": "posts.user_id"}},
						},
					},
				},
			},
			check: func(t *testing.T, cjq *CustomJoinQuery) {
				if cjq.BaseTable != "users" {
					t.Fatalf("expected base table users, got %q", cjq.BaseTable)
				}
				if len(cjq.BaseColumns) != 1 || cjq.BaseColumns[0].name != "id" {
					t.Fatalf("unexpected base columns: %+v", cjq.BaseColumns)
				}
				if len(cjq.Joins) != 1 {
					t.Fatalf("expected 1 join, got %d", len(cjq.Joins))
				}
				if len(cjq.JoinedColumns["posts"]) != 2 {
					t.Fatalf("expected 2 joined columns, got %+v", cjq.JoinedColumns["posts"])
				}
			},
		},
		{
			name: "invalid join type",
			query: SelectQuery{
				Join: []JoinClause{
					{
						Table: "posts",
						Type:  "outer",
						On: []map[string]any{
							{"users.id": map[string]any{"eq": "posts.user_id"}},
						},
					},
				},
			},
			wantErr: "invalid join type",
		},
		{
			name: "missing on clause",
			query: SelectQuery{
				Join: []JoinClause{
					{Table: "posts"},
				},
			},
			wantErr: "requires at least one ON condition",
		},
		{
			name: "unknown table in select",
			query: SelectQuery{
				Select: []any{"unknown.id"},
				Join: []JoinClause{
					{
						Table: "posts",
						On: []map[string]any{
							{"users.id": map[string]any{"eq": "posts.user_id"}},
						},
					},
				},
			},
			wantErr: "unknown table in select",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cjq, err := schema.ParseCustomJoinQuery("users", tt.query)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, cjq)
		})
	}
}

func TestParseJoinCondition_AdditionalCases(t *testing.T) {
	tests := []struct {
		name    string
		cond    map[string]any
		wantErr string
	}{
		{
			name: "valid",
			cond: map[string]any{
				"users.id": map[string]any{"eq": "posts.user_id"},
			},
		},
		{
			name: "multiple keys",
			cond: map[string]any{
				"users.id":   map[string]any{"eq": "posts.user_id"},
				"users.name": map[string]any{"eq": "posts.title"},
			},
			wantErr: "exactly one key",
		},
		{
			name: "left side not table column",
			cond: map[string]any{
				"id": map[string]any{"eq": "posts.user_id"},
			},
			wantErr: "left side must be table.column",
		},
		{
			name: "value not object",
			cond: map[string]any{
				"users.id": "posts.user_id",
			},
			wantErr: "value must be an object",
		},
		{
			name: "right side not string",
			cond: map[string]any{
				"users.id": map[string]any{"eq": 1},
			},
			wantErr: "right side must be a column reference string",
		},
		{
			name: "right side not table column",
			cond: map[string]any{
				"users.id": map[string]any{"eq": "user_id"},
			},
			wantErr: "right side must be table.column",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseJoinCondition(tt.cond)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestBuildCustomJoinSelect(t *testing.T) {
	schema := testQuerySchema()

	t.Run("flat join output", func(t *testing.T) {
		cjq := &CustomJoinQuery{
			BaseTable:   "users",
			BaseColumns: []column{{name: "id"}},
			Joins: []customJoin{
				{
					table:    "posts",
					alias:    "posts",
					joinType: JoinTypeLeft,
					flat:     true,
					conditions: []joinCondition{
						{leftTable: "users", leftCol: "id", op: OpEq, rightTable: "posts", rightCol: "user_id"},
					},
				},
			},
			JoinedColumns: map[string][]column{
				"posts": {{name: "title"}},
			},
		}

		query, groupBy, agg, _, err := schema.BuildCustomJoinSelect(cjq, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(groupBy, "GROUP BY") {
			t.Fatalf("did not expect group by for flat join, got %q", groupBy)
		}
		if !strings.Contains(query, "LEFT JOIN [posts]") {
			t.Fatalf("expected LEFT JOIN in query, got %q", query)
		}
		if !strings.Contains(query, "AS [posts_title]") {
			t.Fatalf("expected flattened alias in query, got %q", query)
		}
		if !strings.Contains(agg, "'posts_title'") {
			t.Fatalf("expected flattened aggregation key, got %q", agg)
		}
	})

	t.Run("nested join output", func(t *testing.T) {
		cjq := &CustomJoinQuery{
			BaseTable:   "users",
			BaseColumns: []column{{name: "id"}},
			Joins: []customJoin{
				{
					table:    "posts",
					alias:    "posts",
					joinType: JoinTypeInner,
					flat:     false,
					conditions: []joinCondition{
						{leftTable: "users", leftCol: "id", op: OpEq, rightTable: "posts", rightCol: "user_id"},
					},
				},
			},
			JoinedColumns: map[string][]column{
				"posts": {{name: "title", alias: "post_title"}},
			},
		}

		query, groupBy, agg, _, err := schema.BuildCustomJoinSelect(cjq, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(query, "INNER JOIN [posts]") {
			t.Fatalf("expected INNER JOIN in query, got %q", query)
		}
		if !strings.Contains(query, "json_group_array(") {
			t.Fatalf("expected nested aggregation in query, got %q", query)
		}
		if !strings.Contains(groupBy, "GROUP BY [users].[id]") {
			t.Fatalf("expected group by root id, got %q", groupBy)
		}
		if !strings.Contains(agg, "'posts'") {
			t.Fatalf("expected nested aggregation key, got %q", agg)
		}
	})

	t.Run("missing base table", func(t *testing.T) {
		_, _, _, _, err := schema.BuildCustomJoinSelect(&CustomJoinQuery{
			BaseTable:   "missing",
			BaseColumns: []column{{name: "id"}},
		}, nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("no selected columns", func(t *testing.T) {
		_, _, _, _, err := schema.BuildCustomJoinSelect(&CustomJoinQuery{
			BaseTable:     "users",
			BaseColumns:   []column{},
			JoinedColumns: map[string][]column{},
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "no columns selected") {
			t.Fatalf("expected no columns selected error, got %v", err)
		}
	})
}

func TestBuildSelect_QueryDepthLimitAndRelationshipErrors(t *testing.T) {
	originalDepth := config.Cfg.MaxQueryDepth
	defer func() {
		config.Cfg.MaxQueryDepth = originalDepth
	}()

	schema := testQuerySchema()

	t.Run("query depth exceeds limit", func(t *testing.T) {
		config.Cfg.MaxQueryDepth = 1
		rel := Relation{
			name: "users",
			joins: []*Relation{
				{name: "posts"},
			},
		}

		_, _, _, err := schema.buildSelect(rel, nil)
		if err == nil || !strings.Contains(err.Error(), "query nesting exceeds maximum depth") {
			t.Fatalf("expected depth error, got %v", err)
		}
	})

	t.Run("missing relationship returns error", func(t *testing.T) {
		config.Cfg.MaxQueryDepth = 5
		rel := Relation{
			name:    "users",
			columns: []column{{name: "id"}},
			joins: []*Relation{
				{name: "tags", columns: []column{{name: "name"}}},
			},
		}

		_, _, _, err := schema.buildSelect(rel, nil)
		if err == nil || !strings.Contains(err.Error(), "no relationship exists between tables") {
			t.Fatalf("expected relationship error, got %v", err)
		}
	})
}
