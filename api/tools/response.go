package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// MaxBatchOperations is the maximum number of operations allowed in a batch request.
const MaxBatchOperations = 100

// RespErr writes a structured error response to the ResponseWriter.
func RespErr(w http.ResponseWriter, err error) {
	status, apiErr := BuildAPIError(err)
	RespondJSON(w, status, apiErr)
}

// RespondJSON writes a JSON response with the given status code.
func RespondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// BuildAPIError maps an error to an HTTP status code and structured APIError.
// Returns appropriate status code and error details with diagnostic hints.
func BuildAPIError(err error) (int, APIError) {
	// Map known errors to appropriate status codes, codes, and hints
	switch {
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized, APIError{
			Code:    CodeUnauthorized,
			Message: strings.TrimPrefix(err.Error(), "unauthorized: "),
			Hint:    "Provide a valid session token in the Authorization header.",
		}
	case errors.Is(err, ErrTableNotFound):
		return http.StatusNotFound, APIError{
			Code:    CodeTableNotFound,
			Message: err.Error(),
			Hint:    "Verify the table name is spelled correctly. Use GET /data/schema to list available tables.",
		}
	case errors.Is(err, ErrColumnNotFound):
		return http.StatusNotFound, APIError{
			Code:    CodeColumnNotFound,
			Message: err.Error(),
			Hint:    "Check the column name spelling. Use GET /data/schema to see table columns.",
		}
	case errors.Is(err, ErrDatabaseNotFound):
		return http.StatusNotFound, APIError{
			Code:    CodeDatabaseNotFound,
			Message: err.Error(),
			Hint:    "The database specified in the Database header was not found. Use the platform API or CLI to list all available databases",
		}
	case errors.Is(err, ErrDatabaseOutOfSync):
		return http.StatusConflict, APIError{
			Code:    CodeDatabaseOutOfSync,
			Message: err.Error(),
			Hint:    "The database requested is out of sync with its definition. Use the platform API or CLI to sync it.",
		}
	case errors.Is(err, ErrDefinitionNotFound):
		return http.StatusNotFound, APIError{
			Code:    CodeDefinitionNotFound,
			Message: err.Error(),
			Hint:    "The specified definition does not exist. Use GET /platform/definitions to list available definitions.",
		}
	case errors.Is(err, ErrNoRelationship):
		return http.StatusNotFound, APIError{
			Code:    CodeNoRelationship,
			Message: err.Error(),
			Hint:    "No foreign key relationship exists between these tables. Define a foreign key or query tables separately.",
		}
	case errors.Is(err, ErrDefinitionInUse):
		return http.StatusConflict, APIError{
			Code:    CodeDefinitionInUse,
			Message: err.Error(),
			Hint:    "Remove all databases using this definition before deleting it, or use force=true.",
		}
	case errors.Is(err, ErrInvalidOperator):
		return http.StatusBadRequest, APIError{
			Code:    CodeInvalidOperator,
			Message: err.Error(),
			Hint:    "Valid operators: eq, neq, gt, gte, lt, lte, like, ilike, in, nin, is, not.",
		}
	case errors.Is(err, ErrInvalidColumnType):
		return http.StatusBadRequest, APIError{
			Code:    CodeInvalidColumnType,
			Message: err.Error(),
			Hint:    "Valid column types: TEXT, INTEGER, REAL, BLOB.",
		}
	case errors.Is(err, ErrMissingWhereClause):
		return http.StatusBadRequest, APIError{
			Code:    CodeMissingWhereClause,
			Message: err.Error(),
			Hint:    "DELETE and UPDATE operations require a WHERE clause to prevent accidental data loss.",
		}
	case errors.Is(err, ErrMissingOperation):
		return http.StatusBadRequest, APIError{
			Code:    CodeMissingOperation,
			Message: err.Error(),
			Hint:    "All queries need \"Prefer\": \"operation=\" header to specify select, insert, update, or delete.",
		}
	case errors.Is(err, ErrInvalidOnConflict):
		return http.StatusBadRequest, APIError{
			Code:    CodeInvalidOnConflict,
			Message: err.Error(),
			Hint:    "For Prefer on-conflict header, \"Prefer\": \"on-conflict=\", allowed values are \"\", \"replace\", and \"ignore\"",
		}
	case errors.Is(err, ErrInvalidIdentifier),
		errors.Is(err, ErrEmptyIdentifier),
		errors.Is(err, ErrIdentifierTooLong),
		errors.Is(err, ErrInvalidCharacter):
		return http.StatusBadRequest, APIError{
			Code:    CodeInvalidIdentifier,
			Message: err.Error(),
			Hint:    "Identifiers must start with a letter or underscore, contain only letters, digits, and underscores, and be at most 128 characters.",
		}
	case errors.Is(err, ErrNotDDLQuery):
		return http.StatusBadRequest, APIError{
			Code:    CodeNotDDLQuery,
			Message: err.Error(),
			Hint:    "Only CREATE, ALTER, and DROP statements are allowed for schema modifications.",
		}
	case errors.Is(err, ErrQueryTooDeep):
		return http.StatusBadRequest, APIError{
			Code:    CodeQueryTooDeep,
			Message: err.Error(),
			Hint:    "Reduce the nesting depth of your query by fetching nested data in separate requests.",
		}
	case errors.Is(err, ErrInArrayTooLarge):
		return http.StatusBadRequest, APIError{
			Code:    CodeArrayTooLarge,
			Message: err.Error(),
			Hint:    "Split large IN clauses into multiple smaller queries.",
		}
	case errors.Is(err, ErrBatchTooLarge):
		return http.StatusBadRequest, APIError{
			Code:    CodeBatchTooLarge,
			Message: err.Error(),
			Hint:    fmt.Sprintf("Split the batch into multiple requests with at most %d operations each.", MaxBatchOperations),
		}
	case errors.Is(err, ErrMissingDatabase):
		return http.StatusBadRequest, APIError{
			Code:    CodeMissingDatabase,
			Message: err.Error(),
			Hint:    "Add a 'Database' header with the database name. Use GET /platform/databases to list available databases.",
		}

	// Platform API errors
	case errors.Is(err, ErrInvalidJSON):
		return http.StatusBadRequest, APIError{
			Code:    CodeInvalidJSON,
			Message: err.Error(),
			Hint:    "Check JSON syntax.",
		}
	case errors.Is(err, ErrDefinitionExists):
		return http.StatusConflict, APIError{
			Code:    CodeDefinitionExists,
			Message: err.Error(),
			Hint:    "Choose a different name or delete the existing definition first.",
		}
	case errors.Is(err, ErrNoChanges):
		return http.StatusBadRequest, APIError{
			Code:    CodeNoChanges,
			Message: err.Error(),
			Hint:    "The provided schema is identical to the current version.",
		}
	case errors.Is(err, ErrAtomicbaseBusy):
		return http.StatusConflict, APIError{
			Code:    CodeAtomicbaseBusy,
			Message: err.Error(),
			Hint:    "Wait for the current migration to complete or check job status.",
		}
	case errors.Is(err, ErrDatabaseExists):
		return http.StatusConflict, APIError{
			Code:    CodeDatabaseExists,
			Message: err.Error(),
			Hint:    "Choose a different name or delete the existing database first.",
		}
	case errors.Is(err, ErrDatabaseNotFoundPlatform):
		return http.StatusNotFound, APIError{
			Code:    CodeDatabaseNotFoundPlatform,
			Message: err.Error(),
			Hint:    "Use GET /platform/databases to list available databases.",
		}
	case errors.Is(err, ErrDatabaseInSync):
		return http.StatusBadRequest, APIError{
			Code:    CodeDatabaseInSync,
			Message: err.Error(),
			Hint:    "The database is already at the current definition version.",
		}
	case errors.Is(err, ErrMigrationNotFound):
		return http.StatusNotFound, APIError{
			Code:    CodeMigrationNotFound,
			Message: err.Error(),
			Hint:    "The migration may have been deleted or never existed.",
		}
	case errors.Is(err, ErrVersionNotFound):
		return http.StatusNotFound, APIError{
			Code:    CodeVersionNotFound,
			Message: err.Error(),
			Hint:    "Use GET /platform/definitions/{name}/history to see available versions.",
		}
	case errors.Is(err, ErrInvalidMigration):
		return http.StatusBadRequest, APIError{
			Code:    CodeInvalidMigration,
			Message: err.Error(),
			Hint:    "Check the migration plan for errors.",
		}
	case strings.HasPrefix(err.Error(), "invalid request:"):
		return http.StatusBadRequest, APIError{
			Code:    CodeInvalidRequest,
			Message: strings.TrimPrefix(err.Error(), "invalid request: "),
			Hint:    "Check the request parameters.",
		}

	case errors.Is(err, ErrReservedTable):
		return http.StatusForbidden, APIError{
			Code:    CodeReservedTable,
			Message: err.Error(),
			Hint:    "Tables prefixed with 'atombase_' are reserved for internal use.",
		}
	case errors.Is(err, ErrNoFTSIndex):
		return http.StatusBadRequest, APIError{
			Code:    CodeNoFTSIndex,
			Message: err.Error(),
			Hint:    "Create an FTS5 index on this table before using full-text search. See documentation for FTS setup.",
		}
	case strings.Contains(err.Error(), "UNIQUE constraint failed"):
		return http.StatusConflict, APIError{
			Code:    CodeUniqueViolation,
			Message: "record already exists",
			Hint:    "A record with this unique value already exists. Use upsert (on-conflict=replace) to update existing records.",
		}
	case strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		return http.StatusBadRequest, APIError{
			Code:    CodeForeignKeyViolation,
			Message: "foreign key constraint violation",
			Hint:    "The referenced record does not exist. Ensure the foreign key value points to an existing record.",
		}
	case strings.Contains(err.Error(), "NOT NULL constraint failed"):
		return http.StatusBadRequest, APIError{
			Code:    CodeNotNullViolation,
			Message: "required field is missing",
			Hint:    "One or more required fields were not provided. Check your request body for missing columns.",
		}
	case strings.Contains(err.Error(), "no such table"):
		return http.StatusNotFound, APIError{
			Code:    CodeTableNotFound,
			Message: "table not found",
			Hint:    "The table may not exist or the schema cache may be stale. Use POST /data/schema/invalidate to update the cache.",
		}
	case strings.Contains(err.Error(), "no such column"):
		return http.StatusBadRequest, APIError{
			Code:    CodeColumnNotFound,
			Message: "column not found",
			Hint:    "The column may not exist or the schema cache may be stale. Use POST /data/schema/invalidate to update the cache.",
		}

	// Turso configuration errors
	case strings.Contains(err.Error(), "TURSO_ORGANIZATION is not set"),
		strings.Contains(err.Error(), "TURSO_API_KEY is not set"):
		return http.StatusServiceUnavailable, APIError{
			Code:    CodeTursoConfigMissing,
			Message: "Turso configuration is incomplete",
			Hint:    "Set TURSO_ORGANIZATION and TURSO_API_KEY environment variables. Get these from your Turso dashboard at https://turso.tech/app.",
		}

	// Turso API errors - authentication
	case strings.Contains(err.Error(), "turso API error: 401"):
		return http.StatusUnauthorized, APIError{
			Code:    CodeTursoAuthFailed,
			Message: "Turso authentication failed",
			Hint:    "Your TURSO_API_KEY may be invalid or expired. Generate a new API token from your Turso dashboard.",
		}

	// Turso API errors - forbidden
	case strings.Contains(err.Error(), "turso API error: 403"):
		return http.StatusForbidden, APIError{
			Code:    CodeTursoForbidden,
			Message: "Turso access denied",
			Hint:    "Your API key doesn't have permission for this operation. Check that TURSO_ORGANIZATION matches your token's organization.",
		}

	// Turso API errors - not found
	case strings.Contains(err.Error(), "turso API error: 404"):
		return http.StatusNotFound, APIError{
			Code:    CodeTursoNotFound,
			Message: "Turso resource not found",
			Hint:    "The database or organization doesn't exist in Turso. Verify the database name and that TURSO_ORGANIZATION is correct.",
		}

	// Turso API errors - rate limited
	case strings.Contains(err.Error(), "turso API error: 429"):
		return http.StatusTooManyRequests, APIError{
			Code:    CodeTursoRateLimited,
			Message: "Turso rate limit exceeded",
			Hint:    "You've made too many requests to the Turso API. Wait a moment and retry, or upgrade your Turso plan for higher limits.",
		}

	// Turso API errors - server errors (5xx)
	case strings.Contains(err.Error(), "turso API error: 5"):
		return http.StatusBadGateway, APIError{
			Code:    CodeTursoServerError,
			Message: "Turso service temporarily unavailable",
			Hint:    "The Turso API is experiencing issues. Check https://status.turso.tech for service status and retry later.",
		}

	// libsql token/auth errors
	case strings.Contains(err.Error(), "token") && strings.Contains(err.Error(), "expired"),
		strings.Contains(err.Error(), "JWT") && strings.Contains(err.Error(), "expired"):
		return http.StatusUnauthorized, APIError{
			Code:    CodeTursoTokenExpired,
			Message: "database token has expired",
			Hint:    "The stored database token has expired. Re-register the database to generate a new token, or set TURSO_TOKEN_EXPIRATION=never.",
		}

	// libsql authentication errors
	case strings.Contains(err.Error(), "authentication failed"),
		strings.Contains(err.Error(), "Unauthorized"),
		strings.Contains(err.Error(), "invalid token"):
		return http.StatusUnauthorized, APIError{
			Code:    CodeTursoAuthFailed,
			Message: "database authentication failed",
			Hint:    "The database token is invalid. Re-register the database to generate a new token.",
		}

	// libsql connection errors
	case strings.Contains(err.Error(), "connection refused"),
		strings.Contains(err.Error(), "no such host"),
		strings.Contains(err.Error(), "network is unreachable"),
		strings.Contains(err.Error(), "connection reset"),
		strings.Contains(err.Error(), "i/o timeout"):
		return http.StatusBadGateway, APIError{
			Code:    CodeTursoConnection,
			Message: "failed to connect to database",
			Hint:    "Cannot reach the Turso database. Check your network connection and verify the database exists in your Turso organization.",
		}

	// libsql TLS/certificate errors
	case strings.Contains(err.Error(), "certificate"),
		strings.Contains(err.Error(), "tls:"):
		return http.StatusBadGateway, APIError{
			Code:    CodeTursoConnection,
			Message: "secure connection to database failed",
			Hint:    "TLS connection to Turso failed. This may be a network issue or certificate problem. Retry or check your network configuration.",
		}

	default:
		// For unknown errors, log internally but return generic message
		// Avoid exposing SQL syntax errors, connection details, etc.
		Logger.Error("unhandled error", "error", err.Error())
		return http.StatusInternalServerError, APIError{
			Code:    CodeInternalError,
			Message: "internal server error",
			Hint:    "An unexpected error occurred. Check server logs for details or contact support.",
		}
	}
}
