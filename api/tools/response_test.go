package tools

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildAPIError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "unauthorized sentinel",
			err:        UnauthorizedErr("session expired"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeUnauthorized,
			wantMsg:    "session expired",
		},
		{
			name:       "invalid identifier wraps",
			err:        ValidateIdentifier("1bad"),
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidIdentifier,
			wantMsg:    "identifier contains invalid characters: identifier must start with letter or underscore",
		},
		{
			name:       "table not found sentinel",
			err:        TableNotFoundErr("users"),
			wantStatus: http.StatusNotFound,
			wantCode:   CodeTableNotFound,
			wantMsg:    "table not found in schema: users",
		},
		{
			name:       "column not found sentinel",
			err:        ColumnNotFoundErr("users", "email"),
			wantStatus: http.StatusNotFound,
			wantCode:   CodeColumnNotFound,
			wantMsg:    "column not found in table: email in table users",
		},
		{
			name:       "database not found sentinel",
			err:        ErrDatabaseNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeDatabaseNotFound,
			wantMsg:    ErrDatabaseNotFound.Error(),
		},
		{
			name:       "database out of sync sentinel",
			err:        ErrDatabaseOutOfSync,
			wantStatus: http.StatusConflict,
			wantCode:   CodeDatabaseOutOfSync,
			wantMsg:    ErrDatabaseOutOfSync.Error(),
		},
		{
			name:       "definition not found sentinel",
			err:        ErrDefinitionNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeDefinitionNotFound,
			wantMsg:    ErrDefinitionNotFound.Error(),
		},
		{
			name:       "no relationship sentinel",
			err:        NoRelationshipErr("users", "posts"),
			wantStatus: http.StatusNotFound,
			wantCode:   CodeNoRelationship,
			wantMsg:    "no relationship exists between tables: users and posts",
		},
		{
			name:       "definition in use sentinel",
			err:        ErrDefinitionInUse,
			wantStatus: http.StatusConflict,
			wantCode:   CodeDefinitionInUse,
			wantMsg:    ErrDefinitionInUse.Error(),
		},
		{
			name:       "invalid operator sentinel",
			err:        ErrInvalidOperator,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidOperator,
			wantMsg:    ErrInvalidOperator.Error(),
		},
		{
			name:       "invalid column type sentinel",
			err:        InvalidTypeErr("name", "JSON"),
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidColumnType,
			wantMsg:    "invalid column type: type JSON for column name",
		},
		{
			name:       "missing where sentinel",
			err:        ErrMissingWhereClause,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeMissingWhereClause,
			wantMsg:    ErrMissingWhereClause.Error(),
		},
		{
			name:       "missing operation sentinel",
			err:        ErrMissingOperation,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeMissingOperation,
			wantMsg:    ErrMissingOperation.Error(),
		},
		{
			name:       "invalid on conflict sentinel",
			err:        ErrInvalidOnConflict,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidOnConflict,
			wantMsg:    ErrInvalidOnConflict.Error(),
		},
		{
			name:       "not ddl sentinel",
			err:        ErrNotDDLQuery,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeNotDDLQuery,
			wantMsg:    ErrNotDDLQuery.Error(),
		},
		{
			name:       "query too deep sentinel",
			err:        ErrQueryTooDeep,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeQueryTooDeep,
			wantMsg:    ErrQueryTooDeep.Error(),
		},
		{
			name:       "in array too large sentinel",
			err:        ErrInArrayTooLarge,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeArrayTooLarge,
			wantMsg:    ErrInArrayTooLarge.Error(),
		},
		{
			name:       "invalid request prefix",
			err:        InvalidRequestErr("name is required"),
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidRequest,
			wantMsg:    "name is required",
		},
		{
			name:       "unique constraint string match",
			err:        errors.New("UNIQUE constraint failed: users.email"),
			wantStatus: http.StatusConflict,
			wantCode:   CodeUniqueViolation,
			wantMsg:    "record already exists",
		},
		{
			name:       "foreign key constraint string match",
			err:        errors.New("FOREIGN KEY constraint failed"),
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeForeignKeyViolation,
			wantMsg:    "foreign key constraint violation",
		},
		{
			name:       "not null constraint string match",
			err:        errors.New("NOT NULL constraint failed: users.name"),
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeNotNullViolation,
			wantMsg:    "required field is missing",
		},
		{
			name:       "no such table string match",
			err:        errors.New("no such table: users"),
			wantStatus: http.StatusNotFound,
			wantCode:   CodeTableNotFound,
			wantMsg:    "table not found",
		},
		{
			name:       "no such column string match",
			err:        errors.New("no such column: email"),
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeColumnNotFound,
			wantMsg:    "column not found",
		},
		{
			name:       "platform definition exists",
			err:        ErrDefinitionExists,
			wantStatus: http.StatusConflict,
			wantCode:   CodeDefinitionExists,
			wantMsg:    ErrDefinitionExists.Error(),
		},
		{
			name:       "platform invalid json",
			err:        ErrInvalidJSON,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidJSON,
			wantMsg:    ErrInvalidJSON.Error(),
		},
		{
			name:       "platform no changes",
			err:        ErrNoChanges,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeNoChanges,
			wantMsg:    ErrNoChanges.Error(),
		},
		{
			name:       "platform busy",
			err:        ErrAtomicbaseBusy,
			wantStatus: http.StatusConflict,
			wantCode:   CodeAtomicbaseBusy,
			wantMsg:    ErrAtomicbaseBusy.Error(),
		},
		{
			name:       "platform database exists",
			err:        ErrDatabaseExists,
			wantStatus: http.StatusConflict,
			wantCode:   CodeDatabaseExists,
			wantMsg:    ErrDatabaseExists.Error(),
		},
		{
			name:       "platform database not found",
			err:        ErrDatabaseNotFoundPlatform,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeDatabaseNotFoundPlatform,
			wantMsg:    ErrDatabaseNotFoundPlatform.Error(),
		},
		{
			name:       "platform database in sync",
			err:        ErrDatabaseInSync,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeDatabaseInSync,
			wantMsg:    ErrDatabaseInSync.Error(),
		},
		{
			name:       "platform migration not found",
			err:        ErrMigrationNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeMigrationNotFound,
			wantMsg:    ErrMigrationNotFound.Error(),
		},
		{
			name:       "platform version not found",
			err:        VersionNotFoundErr(7),
			wantStatus: http.StatusNotFound,
			wantCode:   CodeVersionNotFound,
			wantMsg:    "version not found: version 7",
		},
		{
			name:       "platform invalid migration",
			err:        InvalidMigrationErr("rename is ambiguous"),
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidMigration,
			wantMsg:    "invalid migration: rename is ambiguous",
		},
		{
			name:       "reserved table sentinel",
			err:        ErrReservedTable,
			wantStatus: http.StatusForbidden,
			wantCode:   CodeReservedTable,
			wantMsg:    ErrReservedTable.Error(),
		},
		{
			name:       "no fts index sentinel",
			err:        ErrNoFTSIndex,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeNoFTSIndex,
			wantMsg:    ErrNoFTSIndex.Error(),
		},
		{
			name:       "missing database sentinel",
			err:        ErrMissingDatabase,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeMissingDatabase,
			wantMsg:    ErrMissingDatabase.Error(),
		},
		{
			name:       "batch too large sentinel",
			err:        ErrBatchTooLarge,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeBatchTooLarge,
			wantMsg:    ErrBatchTooLarge.Error(),
		},
		{
			name:       "turso config missing",
			err:        errors.New("TURSO_API_KEY is not set"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   CodeTursoConfigMissing,
			wantMsg:    "Turso configuration is incomplete",
		},
		{
			name:       "turso 401 string match",
			err:        errors.New("turso API error: 401"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeTursoAuthFailed,
			wantMsg:    "Turso authentication failed",
		},
		{
			name:       "turso 403 string match",
			err:        errors.New("turso API error: 403"),
			wantStatus: http.StatusForbidden,
			wantCode:   CodeTursoForbidden,
			wantMsg:    "Turso access denied",
		},
		{
			name:       "turso 404 string match",
			err:        errors.New("turso API error: 404"),
			wantStatus: http.StatusNotFound,
			wantCode:   CodeTursoNotFound,
			wantMsg:    "Turso resource not found",
		},
		{
			name:       "turso 429 string match",
			err:        errors.New("turso API error: 429"),
			wantStatus: http.StatusTooManyRequests,
			wantCode:   CodeTursoRateLimited,
			wantMsg:    "Turso rate limit exceeded",
		},
		{
			name:       "turso 5xx string match",
			err:        errors.New("turso API error: 500"),
			wantStatus: http.StatusBadGateway,
			wantCode:   CodeTursoServerError,
			wantMsg:    "Turso service temporarily unavailable",
		},
		{
			name:       "expired database token",
			err:        errors.New("JWT token expired"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeTursoTokenExpired,
			wantMsg:    "database token has expired",
		},
		{
			name:       "authentication failed",
			err:        errors.New("authentication failed"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeTursoAuthFailed,
			wantMsg:    "database authentication failed",
		},
		{
			name:       "invalid database token",
			err:        errors.New("invalid token"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeTursoAuthFailed,
			wantMsg:    "database authentication failed",
		},
		{
			name:       "connection refused",
			err:        errors.New("connection refused"),
			wantStatus: http.StatusBadGateway,
			wantCode:   CodeTursoConnection,
			wantMsg:    "failed to connect to database",
		},
		{
			name:       "tls failure",
			err:        errors.New("tls: handshake failure"),
			wantStatus: http.StatusBadGateway,
			wantCode:   CodeTursoConnection,
			wantMsg:    "secure connection to database failed",
		},
		{
			name:       "unknown error fallback",
			err:        errors.New("boom"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   CodeInternalError,
			wantMsg:    "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, apiErr := BuildAPIError(tt.err)
			if status != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, status)
			}
			if apiErr.Code != tt.wantCode {
				t.Fatalf("expected code %s, got %s", tt.wantCode, apiErr.Code)
			}
			if apiErr.Message != tt.wantMsg {
				t.Fatalf("expected message %q, got %q", tt.wantMsg, apiErr.Message)
			}
			if apiErr.Hint == "" {
				t.Fatal("expected non-empty hint")
			}
		})
	}
}

func TestRespErr(t *testing.T) {
	rec := httptest.NewRecorder()

	RespErr(rec, ErrMissingDatabase)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content-type application/json, got %q", got)
	}

	var apiErr APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if apiErr.Code != CodeMissingDatabase {
		t.Fatalf("expected code %s, got %s", CodeMissingDatabase, apiErr.Code)
	}
}
