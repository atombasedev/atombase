// Package data provides the Data API for database operations.
package data

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PrimaryDao wraps Database for operations on the primary/local database.
type PrimaryDao struct {
	Database
}

// Database represents a connection to a SQLite/Turso database with cached schema.
type Database struct {
	Client          *sql.DB     // SQL database connection
	Schema          SchemaCache // Cached schema for validation
	ID              int32       // Internal database ID (1 = primary)
	TemplateID      int32       // Template ID (0 for primary database)
	SchemaVersion   int         // Current template version from schema cache
	DatabaseVersion int         // Database's applied template_version
}

// SchemaCache holds cached table and foreign key information for query validation.
type SchemaCache struct {
	Tables    map[string]CacheTable // Keyed by table name
	Fks       map[string][]CacheFk  // Keyed by table name -> list of FKs from that table
	FTSTables map[string]bool       // Set of tables that have FTS5 indexes
}

// Fk represents a foreign key relationship between tables.
type CacheFk struct {
	Table      string // Table containing the FK column
	References string // Referenced table
	From       string // FK column name
	To         string // Referenced column name
}

type CacheTable struct {
	Name    string            `json:"name"`
	Pk      []string          `json:"pk"`
	Columns map[string]string `json:"columns"`
}

// Table represents a database table's schema.
type Table struct {
	Name       string         `json:"name"`                 // Table name
	Pk         []string       `json:"pk"`                   // Primary key column name(s) - supports composite keys
	Columns    map[string]Col `json:"columns"`              // Keyed by column name
	Indexes    []Index        `json:"indexes,omitempty"`    // Table indexes
	FTSColumns []string       `json:"ftsColumns,omitempty"` // Columns for FTS5 full-text search
}

// Index represents a database index definition.
type Index struct {
	Name    string   `json:"name"`    // Index name
	Columns []string `json:"columns"` // Columns included in index
	Unique  bool     `json:"unique,omitempty"`
}

// Col represents a column definition.
type Col struct {
	Name       string     `json:"name"`                 // Column name
	Type       string     `json:"type"`                 // SQLite type (TEXT, INTEGER, REAL, BLOB)
	NotNull    bool       `json:"notNull,omitempty"`    // NOT NULL constraint
	Unique     bool       `json:"unique,omitempty"`     // UNIQUE constraint
	Default    any        `json:"default,omitempty"`    // Default value (nil if none)
	Collate    string     `json:"collate,omitempty"`    // COLLATE: BINARY, NOCASE, RTRIM
	Check      string     `json:"check,omitempty"`      // CHECK constraint expression
	Generated  *Generated `json:"generated,omitempty"`  // Generated column definition
	References string     `json:"references,omitempty"` // Foreign key reference (format: "table.column")
	OnDelete   string     `json:"onDelete,omitempty"`   // FK action: CASCADE, SET NULL, RESTRICT, NO ACTION
	OnUpdate   string     `json:"onUpdate,omitempty"`   // FK action: CASCADE, SET NULL, RESTRICT, NO ACTION
}

// Generated represents a generated/computed column.
type Generated struct {
	Expr   string `json:"expr"`             // Expression to compute value
	Stored bool   `json:"stored,omitempty"` // true=STORED, false=VIRTUAL (default)
}

// SchemaTemplate represents a reusable schema template for multi-tenant databases.
// Daughter databases associated with a template will inherit its schema.
type SchemaTemplate struct {
	ID             int32   `json:"id"`
	Name           string  `json:"name"`
	Tables         []Table `json:"tables"`         // Table definitions for this template
	CurrentVersion int     `json:"currentVersion"` // Current schema version
	CreatedAt      string  `json:"createdAt"`      // ISO timestamp
	UpdatedAt      string  `json:"updatedAt"`      // ISO timestamp
}

// TemplateVersion represents a historical version of a schema template.
type TemplateVersion struct {
	ID         int32   `json:"id"`
	TemplateID int32   `json:"templateId"`
	Version    int     `json:"version"`
	Tables     []Table `json:"tables"`
	Checksum   string  `json:"checksum"`
	Changes    string  `json:"changes,omitempty"` // JSON description of changes from previous version
	CreatedAt  string  `json:"createdAt"`
}

// SchemaChange represents a single change between schema versions.
type SchemaChange struct {
	Type        string `json:"type"`                // add_table, drop_table, add_column, drop_column, modify_column, rename_column, rename_table
	Table       string `json:"table"`               // Table name
	Column      string `json:"column,omitempty"`    // Column name (for column changes)
	SQL         string `json:"sql,omitempty"`       // SQL statement to apply this change
	RequiresMig bool   `json:"requiresMigration"`   // Whether this change requires data migration
	Ambiguous   bool   `json:"ambiguous,omitempty"` // Whether this change needs user confirmation
	OldName     string `json:"oldName,omitempty"`   // For potential renames, the old name
	Reason      string `json:"reason,omitempty"`    // Explanation for ambiguous changes
}

// Executor is an interface that both *sql.DB and *sql.Tx implement.
// This allows query methods to work with either a direct connection or a transaction.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// RowData represents insert/upsert data that can be either a single object or an array.
// It normalizes to a slice internally for consistent handling.
type RowData []map[string]any

// UnmarshalJSON implements custom unmarshaling to accept both object and array.
func (r *RowData) UnmarshalJSON(data []byte) error {
	// Try array first
	var arr []map[string]any
	if err := json.Unmarshal(data, &arr); err == nil {
		*r = arr
		return nil
	}

	// Try single object
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		*r = []map[string]any{obj}
		return nil
	}

	return json.Unmarshal(data, &arr) // Return original array error
}

// SelectQuery represents a JSON SELECT query request body.
// Used with POST /data/query/{table} and Prefer: operation=select header.
type SelectQuery struct {
	Select []any             `json:"select,omitempty"` // Columns: ["id", "name", {"posts": ["title"]}]
	Join   []JoinClause      `json:"join,omitempty"`   // Custom joins: [{"table": "orders", "on": [...]}]
	Where  []map[string]any  `json:"where,omitempty"`  // Filters: [{"id": {"eq": 5}}, {"or": [...]}]
	Order  map[string]string `json:"order,omitempty"`  // Ordering: {"created_at": "desc"}
	Limit  *int              `json:"limit,omitempty"`
	Offset *int              `json:"offset,omitempty"`
}

// JoinClause represents a custom join specification.
// Used for explicit joins where FK relationships don't exist or custom conditions are needed.
type JoinClause struct {
	Table string           `json:"table"`           // Table to join
	Type  string           `json:"type,omitempty"`  // "left" or "inner", default "left"
	On    []map[string]any `json:"on"`              // Join conditions: [{"users.id": {"eq": "orders.user_id"}}]
	Alias string           `json:"alias,omitempty"` // Optional alias for the joined table
	Flat  bool             `json:"flat,omitempty"`  // If true, flatten output instead of nesting
}

// InsertRequest represents a JSON INSERT request body.
// Used with POST /data/query/{table} with Prefer: operation=insert header.
// Data accepts either a single object or an array of objects.
type InsertRequest struct {
	Data      RowData  `json:"data"`                // Row(s) to insert: {...} or [{...}, ...]
	Returning []string `json:"returning,omitempty"` // Columns to return after insert
}

// UpsertRequest represents a JSON UPSERT request body.
// Used with POST /data/query/{table} and Prefer: operation=insert,on-conflict=replace header.
// Data accepts either a single object or an array of objects.
type UpsertRequest struct {
	Data      RowData  `json:"data"`                // Row(s) to upsert: {...} or [{...}, ...]
	Returning []string `json:"returning,omitempty"` // Columns to return after upsert
}

// UpdateRequest represents a JSON UPDATE request body.
// Used with PATCH /data/query/{table}.
type UpdateRequest struct {
	Data  map[string]any   `json:"data"`  // Column values to update
	Where []map[string]any `json:"where"` // Required: filter conditions
}

// DeleteRequest represents a JSON DELETE request body.
// Used with DELETE /data/query/{table}.
type DeleteRequest struct {
	Where []map[string]any `json:"where"` // Required: filter conditions
}

// Filter represents a single filter condition on a column.
// Only one field should be set per filter.
type Filter struct {
	Eq      any     `json:"eq,omitempty"`
	Neq     any     `json:"neq,omitempty"`
	Gt      any     `json:"gt,omitempty"`
	Gte     any     `json:"gte,omitempty"`
	Lt      any     `json:"lt,omitempty"`
	Lte     any     `json:"lte,omitempty"`
	Like    string  `json:"like,omitempty"`
	Glob    string  `json:"glob,omitempty"`
	In      []any   `json:"in,omitempty"`
	Between []any   `json:"between,omitempty"` // Exactly 2 elements
	Is      any     `json:"is,omitempty"`      // null, true, false
	Fts     string  `json:"fts,omitempty"`     // Full-text search query
	Not     *Filter `json:"not,omitempty"`     // Negation wrapper
}

// BatchRequest represents a JSON batch request body.
// Used with POST /data/batch.
type BatchRequest struct {
	Operations []BatchOperation `json:"operations"`
}

// BatchOperation represents a single operation within a batch.
type BatchOperation struct {
	Operation string         `json:"operation"` // select, insert, upsert, update, delete
	Table     string         `json:"table"`
	Body      map[string]any `json:"body"`  // Operation-specific body
	Count     bool           `json:"count"` // Include count in select results (for count/withCount modes)
}

// BatchResponse represents the response from a batch request.
type BatchResponse struct {
	Results []any `json:"results"`
}

// SelectResult holds the result of a Select query with optional count.
type SelectResult struct {
	Data  []byte
	Count int64
}

// Prefer header values
const (
	PreferOperationSelect   = "operation=select"
	PreferOnConflictReplace = "on-conflict=replace"
	PreferOnConflictIgnore  = "on-conflict=ignore"
	PreferCountExact        = "count=exact"
)
