package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/tools"
)

// withBody wraps handlers that read request bodies to enforce size limits.
func withBody(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, config.Cfg.MaxRequestBody)
		defer r.Body.Close()
		handler(w, r)
	}
}

func validateResourceName(name string) (code, message, hint string) {
	if len(name) == 0 {
		return tools.CodeInvalidName, "name cannot be empty",
			"Names must be 1-64 characters, containing only lowercase letters, numbers, and dashes."
	}
	if len(name) > 64 {
		return tools.CodeInvalidName, "name exceeds maximum length of 64 characters",
			"Names must be 1-64 characters, containing only lowercase letters, numbers, and dashes."
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return tools.CodeInvalidName, "name contains invalid characters",
				"Names must contain only lowercase letters, numbers, and dashes."
		}
	}
	return "", "", ""
}

// encodeSchemaForStorage encodes schema for database storage.
func encodeSchemaForStorage(schema Schema) ([]byte, error) {
	return tools.EncodeSchema(schema)
}

// RegisterRoutes registers all platform API routes.
func (api *API) RegisterRoutes(mux *http.ServeMux) {
	// Templates
	mux.HandleFunc("GET /platform/templates", api.handleListTemplates)
	mux.HandleFunc("GET /platform/templates/{name}", api.handleGetTemplate)
	mux.HandleFunc("POST /platform/templates", withBody(api.handleCreateTemplate))
	mux.HandleFunc("DELETE /platform/templates/{name}", api.handleDeleteTemplate)
	mux.HandleFunc("POST /platform/templates/{name}/diff", withBody(api.handleDiffTemplate))
	mux.HandleFunc("POST /platform/templates/{name}/migrate", withBody(api.handleMigrateTemplate))
	mux.HandleFunc("GET /platform/templates/{name}/history", api.handleGetTemplateHistory)

	// Databases
	mux.HandleFunc("GET /platform/databases", api.handleListDatabases)
	mux.HandleFunc("GET /platform/databases/{name}", api.handleGetDatabase)
	mux.HandleFunc("POST /platform/databases", withBody(api.handleCreateDatabase))
	mux.HandleFunc("DELETE /platform/databases/{name}", api.handleDeleteDatabase)
	mux.HandleFunc("POST /platform/databases/{name}/sync", api.handleSyncDatabase)

}

// =============================================================================
// Template Handlers
// =============================================================================

func (api *API) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := api.listTemplates(r.Context())
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusOK, templates)
}

func (api *API) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("template name is required"))
		return
	}

	template, err := api.getTemplate(r.Context(), name)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusOK, template)
}

func (api *API) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req CreateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}

	if req.Name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("name is required"))
		return
	}

	if code, msg, _ := validateResourceName(req.Name); code != "" {
		tools.RespErr(w, tools.InvalidRequestErr(msg))
		return
	}

	if len(req.Schema.Tables) == 0 {
		tools.RespErr(w, tools.InvalidRequestErr("schema must have at least one table"))
		return
	}

	template, err := api.createTemplate(r.Context(), req.Name, req.Schema)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, template)
}

func (api *API) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("template name is required"))
		return
	}

	err := api.deleteTemplate(r.Context(), name)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (api *API) handleDiffTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("template name is required"))
		return
	}

	var req DiffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}

	result, err := api.diffTemplate(r.Context(), name, req.Schema)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (api *API) handleMigrateTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("template name is required"))
		return
	}

	var req MigrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}

	ctx := r.Context()

	template, err := api.getTemplate(ctx, name)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	changes := diffSchemas(template.Schema, req.Schema)
	if len(changes) == 0 {
		tools.RespErr(w, tools.ErrNoChanges)
		return
	}

	plan, err := GenerateMigrationPlan(template.Schema, req.Schema, changes, req.Merge)
	if err != nil {
		tools.RespErr(w, tools.InvalidMigrationErr(err.Error()))
		return
	}

	databases, err := api.getDatabasesByTemplate(ctx, template.ID)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	// Validate FK references (no DB needed)
	validationResult, err := ValidateMigrationPlan(ctx, req.Schema, nil)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	if !validationResult.Valid {
		errMsg := validationResult.Errors[0].Message
		if len(validationResult.Errors) > 1 {
			errMsg = fmt.Sprintf("%d errors: %s", len(validationResult.Errors), errMsg)
		}
		tools.RespErr(w, fmt.Errorf("validation failed: %s", errMsg))
		return
	}

	newVersion := template.CurrentVersion + 1
	schemaJSON, err := encodeSchemaForStorage(req.Schema)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	hash := sha256.Sum256(schemaJSON)
	checksum := hex.EncodeToString(hash[:])

	conn, err := api.dbConn()
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if len(databases) > 0 {
		// Get token for test database
		token, err := api.getDatabaseToken(ctx, databases[0].Name)
		if err != nil {
			tools.RespErr(w, fmt.Errorf("failed to get token for test database: %w", err))
			return
		}

		execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = BatchExecuteWithToken(execCtx, databases[0].Name, token, plan.SQL)
		cancel()
		if err != nil {
			respondJSON(w, http.StatusBadRequest, tools.APIError{
				Code:    "MIGRATION_FAILED",
				Message: fmt.Sprintf("Migration failed on test database '%s': %v", databases[0].Name, err),
				Hint:    "Fix the schema and try again. No databases were modified.",
			})
			return
		}

		if err := api.updateDatabaseVersion(ctx, databases[0].ID, newVersion); err != nil {
			tools.RespErr(w, err)
			return
		}
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		tools.RespErr(w, err)
		return
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (template_id, version, schema, checksum, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, TableTemplatesHistory), template.ID, newVersion, schemaJSON, checksum, now)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	migrationSQL, err := json.Marshal(plan.SQL)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (template_id, from_version, to_version, sql, status, created_at)
		VALUES (?, ?, ?, ?, 'ready', ?)
	`, TableMigrations), template.ID, template.CurrentVersion, newVersion, string(migrationSQL), now)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET current_version = ?, updated_at = ? WHERE id = ?
	`, TableTemplates), newVersion, now, template.ID)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	if err := tx.Commit(); err != nil {
		tools.RespErr(w, err)
		return
	}

	tools.InvalidateTemplate(template.ID)

	respondJSON(w, http.StatusOK, MigrateResponse{
		TemplateID:     template.ID,
		CurrentVersion: newVersion,
		DatabasesTotal: len(databases),
		Status:         "ready",
	})
}

func (api *API) handleGetTemplateHistory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("template name is required"))
		return
	}

	history, err := api.getTemplateHistory(r.Context(), name)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusOK, history)
}

// =============================================================================
// Database Handlers
// =============================================================================

func (api *API) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	databases, err := api.listDatabases(r.Context())
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusOK, databases)
}

func (api *API) handleGetDatabase(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("database name is required"))
		return
	}

	database, err := api.getDatabase(r.Context(), name)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusOK, database)
}

func (api *API) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var req CreateDatabaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		tools.RespErr(w, tools.ErrInvalidJSON)
		return
	}

	if req.Name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("name is required"))
		return
	}

	if code, msg, _ := validateResourceName(req.Name); code != "" {
		tools.RespErr(w, tools.InvalidRequestErr(msg))
		return
	}

	if req.Template == "" {
		tools.RespErr(w, tools.InvalidRequestErr("template is required"))
		return
	}

	database, err := api.createDatabase(r.Context(), req.Name, req.Template)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, database)
}

func (api *API) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("database name is required"))
		return
	}

	err := api.deleteDatabase(r.Context(), name)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (api *API) handleSyncDatabase(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		tools.RespErr(w, tools.InvalidRequestErr("database name is required"))
		return
	}

	result, err := api.syncDatabase(r.Context(), name)
	if err != nil {
		tools.RespErr(w, err)
		return
	}

	respondJSON(w, http.StatusOK, result)
}

// =============================================================================
// Response Helpers
// =============================================================================

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
