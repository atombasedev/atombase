package platform

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/tools"
)

// Re-export errors from tools for backward compatibility.
var (
	ErrDatabaseNotFound = tools.ErrDatabaseNotFoundPlatform
	ErrDatabaseExists   = tools.ErrDatabaseExists
	ErrDatabaseInSync   = tools.ErrDatabaseInSync
)

// listDatabases returns all databases.
func (api *API) listDatabases(ctx context.Context) ([]Database, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, name, template_id, template_version, created_at, updated_at
		FROM %s
		ORDER BY name
	`, TableDatabases))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var databases []Database
	for rows.Next() {
		var t Database
		var createdAt, updatedAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.TemplateID, &t.TemplateVersion, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		// Token is omitted in list responses
		databases = append(databases, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if databases == nil {
		databases = []Database{}
	}

	return databases, nil
}

// getDatabase returns a database by name.
func (api *API) getDatabase(ctx context.Context, name string) (*Database, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	row := conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, name, template_id, template_version, created_at, updated_at
		FROM %s
		WHERE name = ?
	`, TableDatabases), name)

	var t Database
	var createdAt, updatedAt string

	if err := row.Scan(&t.ID, &t.Name, &t.TemplateID, &t.TemplateVersion, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDatabaseNotFound
		}
		return nil, err
	}

	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &t, nil
}

// getDatabaseToken retrieves the decrypted auth token for a database.
// Checks cache first, falls back to database lookup with decryption.
func (api *API) getDatabaseToken(ctx context.Context, name string) (string, error) {
	// Check cache first
	if cached, ok := tools.GetDatabase(name); ok && cached.AuthToken != "" {
		return cached.AuthToken, nil
	}

	// Fall back to database query
	conn, err := api.dbConn()
	if err != nil {
		return "", err
	}

	var encryptedToken []byte
	err = conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT auth_token_encrypted FROM %s WHERE name = ?
	`, TableDatabases), name).Scan(&encryptedToken)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrDatabaseNotFound
		}
		return "", err
	}

	if len(encryptedToken) == 0 {
		return "", fmt.Errorf("database has no auth token configured")
	}

	if !tools.EncryptionEnabled() {
		return "", fmt.Errorf("encryption not initialized")
	}

	decrypted, err := tools.Decrypt(encryptedToken)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt token: %w", err)
	}

	return string(decrypted), nil
}

// createDatabase creates a new database using the specified template.
// Creates a Turso database, generates a token, and initializes the schema.
func (api *API) createDatabase(ctx context.Context, name, templateName string) (*Database, error) {
	// Get template to verify it exists and get current version
	template, err := api.getTemplate(ctx, templateName)
	if err != nil {
		return nil, err
	}

	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	// Check if database already exists
	var exists int
	err = conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*) FROM %s WHERE name = ?
	`, TableDatabases), name).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if exists > 0 {
		return nil, ErrDatabaseExists
	}

	// Create Turso database
	if err := tursocreateDatabase(ctx, name); err != nil {
		return nil, fmt.Errorf("failed to create turso database: %w", err)
	}

	// Create auth token for the database
	token, err := tursoCreateToken(ctx, name)
	if err != nil {
		_ = tursodeleteDatabase(ctx, name)
		return nil, fmt.Errorf("failed to create database token: %w", err)
	}

	// Encrypt the token
	var encryptedToken []byte
	if tools.EncryptionEnabled() {
		encryptedToken, err = tools.Encrypt([]byte(token))
		if err != nil {
			_ = tursodeleteDatabase(ctx, name)
			return nil, fmt.Errorf("failed to encrypt database token: %w", err)
		}
	}

	// Initialize database with template schema
	// Retry with backoff since newly created Turso databases may not be immediately available
	migrationSQL := generateSchemaSQL(template.Schema)
	var schemaErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		schemaErr = BatchExecuteWithToken(ctx, name, token, migrationSQL)
		if schemaErr == nil {
			break
		}
		// Only retry on 404 errors (database not yet available)
		if !strings.Contains(schemaErr.Error(), "404") {
			break
		}
	}
	if schemaErr != nil {
		// Try to clean up
		_ = tursodeleteDatabase(ctx, name)
		return nil, fmt.Errorf("failed to initialize database schema: %w", schemaErr)
	}

	// Insert database record
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := conn.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (name, template_id, template_version, auth_token_encrypted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, TableDatabases), name, template.ID, template.CurrentVersion, encryptedToken, now, now)
	if err != nil {
		// Try to clean up
		_ = tursodeleteDatabase(ctx, name)
		return nil, err
	}

	tenantID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	createdAt, _ := time.Parse(time.RFC3339, now)

	return &Database{
		ID:              int32(tenantID),
		Name:            name,
		TemplateID:      template.ID,
		TemplateVersion: template.CurrentVersion,
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt,
	}, nil
}

// deleteDatabase deletes a database and its Turso database.
func (api *API) deleteDatabase(ctx context.Context, name string) error {
	conn, err := api.dbConn()
	if err != nil {
		return err
	}

	// Check if database exists
	var tenantID int32
	err = conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id FROM %s WHERE name = ?
	`, TableDatabases), name).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDatabaseNotFound
		}
		return err
	}

	// Delete Turso database
	if err := tursodeleteDatabase(ctx, name); err != nil {
		return fmt.Errorf("failed to delete turso database: %w", err)
	}

	// Delete database record
	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		DELETE FROM %s WHERE id = ?
	`, TableDatabases), tenantID)
	if err != nil {
		return err
	}

	// Invalidate cache
	tools.InvalidateDatabase(name)

	return nil
}

// syncDatabase synchronizes a database to the template's current version.
// Applies chained migrations if needed (e.g., v1->v2->v3).
// This is synchronous - waits for completion.
func (api *API) syncDatabase(ctx context.Context, name string) (*SyncDatabaseResponse, error) {
	// Get database
	database, err := api.getDatabase(ctx, name)
	if err != nil {
		return nil, err
	}

	// Get template to find current version
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	var currentVersion int
	err = conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT current_version FROM %s WHERE id = ?
	`, TableTemplates), database.TemplateID).Scan(&currentVersion)
	if err != nil {
		return nil, err
	}

	// Check if already at current version
	if database.TemplateVersion >= currentVersion {
		return nil, ErrDatabaseInSync
	}

	fromVersion := database.TemplateVersion

	// Get auth token for this database
	token, err := api.getDatabaseToken(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get database token: %w", err)
	}

	// Collect all migration SQL from database's version to current
	var allSQL []string
	for v := database.TemplateVersion; v < currentVersion; v++ {
		migration, err := api.getMigrationByVersions(ctx, database.TemplateID, v, v+1)
		if err != nil {
			return nil, fmt.Errorf("migration from v%d to v%d not found: %w", v, v+1, err)
		}
		allSQL = append(allSQL, migration.SQL...)
	}

	// Execute all migrations
	if len(allSQL) > 0 {
		if err := BatchExecuteWithToken(ctx, database.Name, token, allSQL); err != nil {
			return nil, fmt.Errorf("migration failed: %w", err)
		}
	}

	// Update database version
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET template_version = ?, updated_at = ? WHERE id = ?
	`, TableDatabases), currentVersion, now, database.ID)
	if err != nil {
		return nil, err
	}

	// Update cache with new version
	tools.UpdateDatabaseVersion(name, currentVersion)

	return &SyncDatabaseResponse{
		FromVersion: fromVersion,
		ToVersion:   currentVersion,
	}, nil
}

// getMigrationByVersions returns a migration for a specific version transition.
func (api *API) getMigrationByVersions(ctx context.Context, templateID int32, fromVersion, toVersion int) (*Migration, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	row := conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, template_id, from_version, to_version, sql, status, created_at
		FROM %s
		WHERE template_id = ? AND from_version = ? AND to_version = ?
	`, TableMigrations), templateID, fromVersion, toVersion)

	var m Migration
	var sqlJSON string
	var createdAt string

	if err := row.Scan(
		&m.ID, &m.TemplateID, &m.FromVersion, &m.ToVersion, &sqlJSON,
		&m.Status, &createdAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("migration not found")
		}
		return nil, err
	}

	if err := json.Unmarshal([]byte(sqlJSON), &m.SQL); err != nil {
		return nil, fmt.Errorf("failed to decode migration SQL: %w", err)
	}

	m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)

	return &m, nil
}

// getDatabasesByTemplate returns all databases using a specific template.
func (api *API) getDatabasesByTemplate(ctx context.Context, templateID int32) ([]Database, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, name, template_id, template_version, created_at, updated_at
		FROM %s
		WHERE template_id = ?
		ORDER BY name
	`, TableDatabases), templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var databases []Database
	for rows.Next() {
		var t Database
		var createdAt, updatedAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.TemplateID, &t.TemplateVersion, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		databases = append(databases, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if databases == nil {
		databases = []Database{}
	}

	return databases, nil
}

// getPendingDatabases returns databases that need migration for a given job.
// Pending = template_version < target.
func (api *API) getPendingDatabases(ctx context.Context, _ int64, templateID int32, targetVersion int) ([]Database, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(ctx, fmt.Sprintf(`
		SELECT t.id, t.name, t.template_id, t.template_version, t.created_at, t.updated_at
		FROM %s t
		WHERE t.template_id = ?
		  AND t.template_version < ?
		ORDER BY t.name
	`, TableDatabases), templateID, targetVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var databases []Database
	for rows.Next() {
		var t Database
		var createdAt, updatedAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.TemplateID, &t.TemplateVersion, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		databases = append(databases, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if databases == nil {
		databases = []Database{}
	}

	return databases, nil
}

// getFailedDatabases returns databases that failed migration for a given job.
func (api *API) getFailedDatabases(ctx context.Context, migrationID int64) ([]Database, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	migration, err := api.getMigration(ctx, migrationID)
	if err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(ctx, fmt.Sprintf(`
		SELECT t.id, t.name, t.template_id, t.template_version, t.created_at, t.updated_at
		FROM %s t
		JOIN %s mf ON mf.database_id = t.id
		WHERE t.template_id = ?
		  AND mf.from_version = ?
		  AND mf.to_version = ?
		ORDER BY t.name
	`, TableDatabases, TableMigrationFailures), migration.TemplateID, migration.FromVersion, migration.ToVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var databases []Database
	for rows.Next() {
		var t Database
		var createdAt, updatedAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.TemplateID, &t.TemplateVersion, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		databases = append(databases, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if databases == nil {
		databases = []Database{}
	}

	return databases, nil
}

// batchupdateDatabaseVersions updates template_version for multiple databases in one query.
func (api *API) batchUpdateDatabaseVersions(ctx context.Context, tenantIDs []int32, version int) error {
	if len(tenantIDs) == 0 {
		return nil
	}

	conn, err := api.dbConn()
	if err != nil {
		return err
	}

	// Build placeholder string
	placeholders := make([]byte, 0, len(tenantIDs)*2)
	args := make([]any, 0, len(tenantIDs)+2)
	args = append(args, version, time.Now().UTC().Format(time.RFC3339))

	for i, id := range tenantIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, id)
	}

	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET template_version = ?, updated_at = ? WHERE id IN (%s)
	`, TableDatabases, string(placeholders)), args...)

	return err
}

// updateDatabaseVersion updates template_version for a single database.
func (api *API) updateDatabaseVersion(ctx context.Context, databaseID int32, version int) error {
	return api.batchUpdateDatabaseVersions(ctx, []int32{databaseID}, version)
}

// recordDatabaseMigration records the outcome of a database migration.
func (api *API) recordDatabaseMigration(ctx context.Context, migrationID int64, tenantID int32, status, errMsg string) error {
	conn, err := api.dbConn()
	if err != nil {
		return err
	}

	migration, err := api.getMigration(ctx, migrationID)
	if err != nil {
		return err
	}

	if status == DatabaseMigrationStatusSuccess {
		_, err = conn.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s WHERE database_id = ?
		`, TableMigrationFailures), tenantID)
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (database_id, from_version, to_version, error, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(database_id) DO UPDATE SET
			from_version = excluded.from_version,
			to_version = excluded.to_version,
			error = excluded.error,
			created_at = excluded.created_at
	`, TableMigrationFailures), tenantID, migration.FromVersion, migration.ToVersion, errMsg, now)

	return err
}

// generateSchemaSQL generates the SQL statements to create a schema from scratch.
func generateSchemaSQL(schema Schema) []string {
	var sql []string

	// Enable foreign keys
	sql = append(sql, "PRAGMA foreign_keys = ON")

	// Create tables
	for _, table := range schema.Tables {
		sql = append(sql, generateCreateTableSQL(table))

		// Create indexes
		for _, idx := range table.Indexes {
			sql = append(sql, generateCreateIndexSQL(table.Name, idx))
		}

		// Create FTS if configured
		if len(table.FTSColumns) > 0 {
			sql = append(sql, generateFTSSQL(table.Name, table.FTSColumns, table.Pk)...)
		}
	}

	return sql
}

// =============================================================================
// Turso API Integration
// =============================================================================

// tursocreateDatabase creates a new database in Turso.
func tursocreateDatabase(ctx context.Context, name string) error {
	org := config.Cfg.TursoOrganization
	apiKey := config.Cfg.TursoAPIKey
	if org == "" || apiKey == "" {
		return fmt.Errorf("TURSO_ORGANIZATION and TURSO_API_KEY must be set")
	}

	body, err := json.Marshal(map[string]string{
		"name":  name,
		"group": config.Cfg.TursoGroup,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases", org)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Read and parse response body
	var respBody bytes.Buffer
	respBody.ReadFrom(resp.Body)

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return fmt.Errorf("turso API error: %s - %s", resp.Status, respBody.String())
	}

	// Parse response to verify database was created
	var createResp struct {
		Database struct {
			Name     string `json:"Name"`
			Hostname string `json:"Hostname"`
		} `json:"database"`
	}
	if err := json.Unmarshal(respBody.Bytes(), &createResp); err != nil {
		return fmt.Errorf("failed to parse turso response: %w (body: %s)", err, respBody.String())
	}

	if createResp.Database.Name == "" {
		return fmt.Errorf("turso returned success but no database name (body: %s)", respBody.String())
	}

	return nil
}

// tursodeleteDatabase deletes a database from Turso.
func tursodeleteDatabase(ctx context.Context, name string) error {
	org := config.Cfg.TursoOrganization
	apiKey := config.Cfg.TursoAPIKey
	if org == "" || apiKey == "" {
		return fmt.Errorf("TURSO_ORGANIZATION and TURSO_API_KEY must be set")
	}

	url := fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases/%s", org, name)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 200 = deleted, 404 = already gone (both OK)
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		var errBody bytes.Buffer
		errBody.ReadFrom(resp.Body)
		return fmt.Errorf("turso API error: %s - %s", resp.Status, errBody.String())
	}

	return nil
}

// tursoCreateToken creates an auth token for a Turso database.
func tursoCreateToken(ctx context.Context, name string) (string, error) {
	org := config.Cfg.TursoOrganization
	apiKey := config.Cfg.TursoAPIKey
	if org == "" || apiKey == "" {
		return "", fmt.Errorf("TURSO_ORGANIZATION and TURSO_API_KEY must be set")
	}

	url := fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases/%s/auth/tokens", org, name)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var respBody bytes.Buffer
	respBody.ReadFrom(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("turso API error: %s - %s", resp.Status, respBody.String())
	}

	var tokenResp struct {
		Jwt string `json:"jwt"`
	}
	if err := json.Unmarshal(respBody.Bytes(), &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse turso token response: %w", err)
	}

	if tokenResp.Jwt == "" {
		return "", fmt.Errorf("turso returned empty token")
	}

	return tokenResp.Jwt, nil
}
