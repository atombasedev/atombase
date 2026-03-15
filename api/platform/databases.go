package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/atombasedev/atombase/definitions"
	"github.com/atombasedev/atombase/tools"
)

var (
	ErrDatabaseNotFound = tools.ErrDatabaseNotFoundPlatform
	ErrDatabaseExists   = tools.ErrDatabaseExists
)

func (api *API) listDatabases(ctx context.Context) ([]DatabaseRecord, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT d.id, d.definition_id, def.name, def.definition_type, d.definition_version, d.created_at, d.updated_at,
		       COALESCE(o.owner_id, ''), COALESCE(o.id, ''), COALESCE(o.name, '')
		FROM atombase_databases d
		JOIN atombase_definitions def ON def.id = d.definition_id
		LEFT JOIN atombase_organizations o ON o.database_id = d.id
		ORDER BY d.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []DatabaseRecord
	for rows.Next() {
		var item DatabaseRecord
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.DefinitionID, &item.DefinitionName, &item.DefinitionType, &item.DefinitionVersion, &createdAt, &updatedAt, &item.OwnerID, &item.OrganizationID, &item.OrganizationName); err != nil {
			return nil, err
		}
		item.CreatedAt = mustParseTime(createdAt)
		item.UpdatedAt = mustParseTime(updatedAt)
		items = append(items, item)
	}
	if items == nil {
		items = []DatabaseRecord{}
	}
	return items, rows.Err()
}

func (api *API) getDatabase(ctx context.Context, id string) (*DatabaseRecord, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	row := conn.QueryRowContext(ctx, `
		SELECT d.id, d.definition_id, def.name, def.definition_type, d.definition_version, d.created_at, d.updated_at,
		       COALESCE(o.owner_id, ''), COALESCE(o.id, ''), COALESCE(o.name, '')
		FROM atombase_databases d
		JOIN atombase_definitions def ON def.id = d.definition_id
		LEFT JOIN atombase_organizations o ON o.database_id = d.id
		WHERE d.id = ?
	`, id)
	var item DatabaseRecord
	var createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.DefinitionID, &item.DefinitionName, &item.DefinitionType, &item.DefinitionVersion, &createdAt, &updatedAt, &item.OwnerID, &item.OrganizationID, &item.OrganizationName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDatabaseNotFound
		}
		return nil, err
	}
	item.CreatedAt = mustParseTime(createdAt)
	item.UpdatedAt = mustParseTime(updatedAt)
	return &item, nil
}

func (api *API) getDatabasesByDefinition(ctx context.Context, definitionID int32) ([]DatabaseRecord, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT d.id, d.definition_id, def.name, def.definition_type, d.definition_version, d.created_at, d.updated_at,
		       COALESCE(o.owner_id, ''), COALESCE(o.id, ''), COALESCE(o.name, '')
		FROM atombase_databases d
		JOIN atombase_definitions def ON def.id = d.definition_id
		LEFT JOIN atombase_organizations o ON o.database_id = d.id
		WHERE d.definition_id = ?
		ORDER BY d.created_at ASC, d.id ASC
	`, definitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []DatabaseRecord
	for rows.Next() {
		var item DatabaseRecord
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.DefinitionID, &item.DefinitionName, &item.DefinitionType, &item.DefinitionVersion, &createdAt, &updatedAt, &item.OwnerID, &item.OrganizationID, &item.OrganizationName); err != nil {
			return nil, err
		}
		item.CreatedAt = mustParseTime(createdAt)
		item.UpdatedAt = mustParseTime(updatedAt)
		items = append(items, item)
	}
	if items == nil {
		items = []DatabaseRecord{}
	}
	return items, rows.Err()
}

func (api *API) getDatabaseToken(ctx context.Context, id string) (string, error) {
	conn, err := api.dbConn()
	if err != nil {
		return "", err
	}
	var encrypted []byte
	if err := conn.QueryRowContext(ctx, `
		SELECT auth_token_encrypted
		FROM atombase_databases
		WHERE id = ?
	`, id).Scan(&encrypted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrDatabaseNotFound
		}
		return "", err
	}
	return decodeStoredDatabaseToken(encrypted)
}

func (api *API) createDatabase(ctx context.Context, req CreateDatabaseRequest) (*DatabaseRecord, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	def, err := api.getDefinition(ctx, req.Definition)
	if err != nil {
		return nil, err
	}

	var exists int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM atombase_databases WHERE id = ?`, req.ID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists > 0 {
		return nil, ErrDatabaseExists
	}

	if err := tursoCreateDatabaseFn(ctx, req.ID); err != nil {
		return nil, fmt.Errorf("failed to create turso database: %w", err)
	}
	token, err := tursoCreateTokenFn(ctx, req.ID)
	if err != nil {
		_ = tursoDeleteDatabaseFn(ctx, req.ID)
		return nil, fmt.Errorf("failed to create database token: %w", err)
	}
	storedToken := []byte(token)
	if tools.EncryptionEnabled() {
		storedToken, err = tools.Encrypt([]byte(token))
		if err != nil {
			_ = tursoDeleteDatabaseFn(ctx, req.ID)
			return nil, err
		}
	}

	var schema Schema
	if err := tools.DecodeSchema(def.Schema, &schema); err != nil {
		_ = tursoDeleteDatabaseFn(ctx, req.ID)
		return nil, err
	}
	if err := batchExecuteWithTokenFn(ctx, req.ID, token, generateSchemaSQL(schema)); err != nil {
		_ = tursodeleteDatabase(ctx, req.ID)
		return nil, fmt.Errorf("failed to initialize database schema: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO atombase_databases (id, definition_id, definition_version, auth_token_encrypted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, req.ID, def.ID, def.CurrentVersion, storedToken, now, now); err != nil {
		return nil, err
	}

	switch def.Type {
	case definitions.DefinitionTypeUser:
		if req.UserID == "" {
			return nil, tools.InvalidRequestErr("userId is required for user definitions")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE atombase_users SET database_id = ?, updated_at = ? WHERE id = ?
		`, req.ID, now, req.UserID); err != nil {
			return nil, err
		}
	case definitions.DefinitionTypeOrganization:
		if req.OrganizationID == "" || req.OrganizationName == "" || req.OwnerID == "" {
			return nil, tools.InvalidRequestErr("organizationId, organizationName, and ownerId are required for organization definitions")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO atombase_organizations (id, database_id, name, owner_id, max_members, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, req.OrganizationID, req.ID, req.OrganizationName, req.OwnerID, req.MaxMembers, now, now); err != nil {
			return nil, err
		}
		membershipSQL := []string{
			`CREATE TABLE IF NOT EXISTS atombase_membership (
				user_id TEXT NOT NULL,
				role TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'active',
				created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY(user_id)
			);`,
		}
		if err := batchExecuteWithTokenFn(ctx, req.ID, token, membershipSQL); err != nil {
			return nil, fmt.Errorf("failed to initialize organization membership table: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return api.getDatabase(ctx, req.ID)
}

func (api *API) deleteDatabase(ctx context.Context, id string) error {
	conn, err := api.dbConn()
	if err != nil {
		return err
	}
	if _, err := api.getDatabase(ctx, id); err != nil {
		return err
	}
	if err := tursoDeleteDatabaseFn(ctx, id); err != nil {
		return fmt.Errorf("failed to delete turso database: %w", err)
	}
	_, err = conn.ExecContext(ctx, `DELETE FROM atombase_databases WHERE id = ?`, id)
	if err == nil {
		tools.InvalidateDatabase(id)
	}
	return err
}
