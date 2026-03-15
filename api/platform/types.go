package platform

import "time"

import "github.com/atombasedev/atombase/definitions"
import sharedschema "github.com/atombasedev/atombase/schema"

type Schema = sharedschema.Schema
type Table = sharedschema.Table
type Index = sharedschema.Index
type Col = sharedschema.Col
type Generated = sharedschema.Generated

type DefinitionType = definitions.DefinitionType
type Definition = definitions.Definition
type Condition = definitions.Condition
type AccessMap = definitions.AccessMap
type OperationPolicy = definitions.OperationPolicy
type DefinitionVersion struct {
	ID           int32     `json:"id"`
	DefinitionID int32     `json:"definitionId"`
	Version      int       `json:"version"`
	Schema       Schema    `json:"schema"`
	Checksum     string    `json:"checksum"`
	CreatedAt    time.Time `json:"createdAt"`
}

type CreateDefinitionRequest struct {
	Name   string                     `json:"name"`
	Type   definitions.DefinitionType `json:"type"`
	Roles  []string                   `json:"roles,omitempty"`
	Schema Schema                     `json:"schema"`
	Access definitions.AccessMap      `json:"access"`
}

type PushDefinitionRequest struct {
	Schema Schema                `json:"schema"`
	Access definitions.AccessMap `json:"access"`
	Merge  []Merge               `json:"merge,omitempty"`
}

// SchemaDiff represents a single schema modification.
type SchemaDiff struct {
	Type string `json:"type"` // add_table, drop_table, rename_table,
	// add_column, drop_column, rename_column, modify_column,
	// add_index, drop_index, add_fts, drop_fts,
	// change_pk_type (requires mirror table)
	Table  string `json:"table,omitempty"`  // Table name
	Column string `json:"column,omitempty"` // Column name (for column changes)
}

// DiffResult is returned by the Diff endpoint with raw changes only.
type DiffResult struct {
	Changes []SchemaDiff `json:"changes"`
}

// Merge indicates a drop+add pair that should be treated as a rename.
// References indices in the changes array.
type Merge struct {
	Old int `json:"old"` // Index of drop statement
	New int `json:"new"` // Index of add statement
}

// MigrationPlan is internal, with all ambiguities resolved, ready to execute.
type MigrationPlan struct {
	SQL []string `json:"sql"` // Generated SQL statements
}

// Migration tracks both the SQL and execution state.
type Migration struct {
	ID           int64      `json:"id"`
	DefinitionID int32      `json:"definitionId"`
	FromVersion  int        `json:"fromVersion"`
	ToVersion    int        `json:"toVersion"`
	SQL          []string   `json:"sql"`    // Migration SQL statements
	Status       string     `json:"status"` // pending, running, paused, complete
	State        *string    `json:"state"`  // null, success, partial, failed
	TotalDBs     int        `json:"totalDbs"`
	CompletedDBs int        `json:"completedDbs"`
	FailedDBs    int        `json:"failedDbs"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// Migration status constants.
const (
	MigrationStatusPending  = "pending"
	MigrationStatusRunning  = "running"
	MigrationStatusPaused   = "paused"
	MigrationStatusComplete = "complete"
)

// Migration state constants.
const (
	MigrationStateSuccess = "success"
	MigrationStatePartial = "partial"
	MigrationStateFailed  = "failed"
)

// DatabaseMigration status constants.
const (
	DatabaseMigrationStatusSuccess = "success"
	DatabaseMigrationStatusFailed  = "failed"
)

// ValidationError represents a pre-migration validation error.
type ValidationError struct {
	Type    string `json:"type"`             // syntax, fk_reference, not_null, unique, check, fk_constraint
	Table   string `json:"table,omitempty"`  // Table name
	Column  string `json:"column,omitempty"` // Column name
	Message string `json:"message"`          // Human-readable error message
	SQL     string `json:"sql,omitempty"`    // SQL that caused the error (for syntax errors)
}

// Template represents a schema template for multi-database systems.
type Template struct {
	ID             int32     `json:"id"`
	Name           string    `json:"name"`
	CurrentVersion int       `json:"currentVersion"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// TemplateWithSchema represents a template with its current schema.
type TemplateWithSchema struct {
	Template
	Schema Schema `json:"schema"`
}

// TemplateVersion represents a historical version of a template's schema.
type TemplateVersion struct {
	ID         int32     `json:"id"`
	TemplateID int32     `json:"templateId"`
	Version    int       `json:"version"`
	Schema     Schema    `json:"schema"`
	Checksum   string    `json:"checksum"`
	CreatedAt  time.Time `json:"createdAt"`
}

// DatabaseRecord represents a provisioned database row returned by the Platform API.
type DatabaseRecord struct {
	ID                string    `json:"id"`
	Token             string    `json:"token"`
	DefinitionID      int32     `json:"definitionId"`
	DefinitionName    string    `json:"definitionName,omitempty"`
	DefinitionType    string    `json:"definitionType,omitempty"`
	DefinitionVersion int       `json:"definitionVersion"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	OwnerID           string    `json:"ownerId,omitempty"`
	OrganizationID    string    `json:"organizationId,omitempty"`
	OrganizationName  string    `json:"organizationName,omitempty"`
}

// Request/Response types for API endpoints.

// CreateTemplateRequest is the request body for POST /platform/templates.
type CreateTemplateRequest struct {
	Name   string `json:"name"`
	Schema Schema `json:"schema"`
}

// DiffRequest is the request body for POST /platform/templates/{name}/diff.
type DiffRequest struct {
	Schema Schema `json:"schema"`
}

// MigrateRequest is the request body for POST /platform/templates/{name}/migrate.
type MigrateRequest struct {
	Schema Schema  `json:"schema"`
	Merge  []Merge `json:"merge,omitempty"` // Indices of drop+add pairs that are renames
}

// MigrateResponse is the response for POST /platform/templates/{name}/migrate.
type MigrateResponse struct {
	TemplateID     int32  `json:"templateId,omitempty"`
	CurrentVersion int    `json:"currentVersion,omitempty"`
	DatabasesTotal int    `json:"databasesTotal,omitempty"`
	Status         string `json:"status,omitempty"`
}

// RetryMigrationResponse is retained for internal compatibility.
// Migration endpoints are deprecated and not exposed.
type RetryMigrationResponse struct {
	RetriedCount int `json:"retriedCount"`
}

// CreateDatabaseRequest is the request body for POST /platform/databases.
type CreateDatabaseRequest struct {
	ID               string `json:"id"`
	Definition       string `json:"definition"`
	UserID           string `json:"userId,omitempty"`
	OrganizationID   string `json:"organizationId,omitempty"`
	OrganizationName string `json:"organizationName,omitempty"`
	OwnerID          string `json:"ownerId,omitempty"`
	MaxMembers       *int   `json:"maxMembers,omitempty"`
}

// SyncDatabaseResponse is the response for POST /platform/databases/{name}/sync.
type SyncDatabaseResponse struct {
	FromVersion int `json:"fromVersion"`
	ToVersion   int `json:"toVersion"`
}
