package platform

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/atombasedev/atombase/definitions"
	"github.com/atombasedev/atombase/tools"
)

var (
	ErrDefinitionNotFound = tools.ErrTemplateNotFound
	ErrDefinitionExists   = tools.ErrTemplateExists
)

func schemaTableSet(schema Schema) map[string]struct{} {
	out := make(map[string]struct{}, len(schema.Tables))
	for _, table := range schema.Tables {
		out[table.Name] = struct{}{}
	}
	return out
}

func (api *API) listDefinitions(ctx context.Context) ([]Definition, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT id, name, definition_type, COALESCE(roles_json, '[]'), current_version, created_at, updated_at
		FROM atombase_definitions
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Definition
	for rows.Next() {
		var item Definition
		var defType string
		var rolesJSON string
		if err := rows.Scan(&item.ID, &item.Name, &defType, &rolesJSON, &item.CurrentVersion, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Type = definitions.DefinitionType(defType)
		_ = json.Unmarshal([]byte(rolesJSON), &item.Roles)
		items = append(items, item)
	}
	if items == nil {
		items = []Definition{}
	}
	return items, rows.Err()
}

func (api *API) getDefinition(ctx context.Context, name string) (*Definition, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	row := conn.QueryRowContext(ctx, `
		SELECT d.id, d.name, d.definition_type, COALESCE(d.roles_json, '[]'), d.current_version, d.created_at, d.updated_at, h.schema_json
		FROM atombase_definitions d
		JOIN atombase_definitions_history h ON h.definition_id = d.id AND h.version = d.current_version
		WHERE d.name = ?
	`, name)
	var item Definition
	var defType string
	var rolesJSON string
	var schemaJSON string
	if err := row.Scan(&item.ID, &item.Name, &defType, &rolesJSON, &item.CurrentVersion, &item.CreatedAt, &item.UpdatedAt, &schemaJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDefinitionNotFound
		}
		return nil, err
	}
	item.Type = definitions.DefinitionType(defType)
	item.Schema = json.RawMessage(schemaJSON)
	_ = json.Unmarshal([]byte(rolesJSON), &item.Roles)
	return &item, nil
}

func (api *API) createDefinition(ctx context.Context, req CreateDefinitionRequest) (*Definition, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	accessRows, err := definitions.ParseAndValidateAccess(req.Type, req.Access, schemaTableSet(req.Schema))
	if err != nil {
		return nil, tools.InvalidRequestErr(err.Error())
	}
	schemaJSON, err := encodeSchemaForStorage(req.Schema)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(schemaJSON)
	checksum := hex.EncodeToString(hash[:])
	rolesJSON, err := json.Marshal(req.Roles)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO atombase_definitions (name, definition_type, roles_json, current_version, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?)
	`, req.Name, string(req.Type), string(rolesJSON), now, now)
	if err != nil {
		if isUniqueConstraintError(err) {
			return nil, ErrDefinitionExists
		}
		return nil, err
	}
	defID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO atombase_definitions_history (definition_id, version, schema_json, checksum, created_at)
		VALUES (?, 1, ?, ?, ?)
	`, defID, string(schemaJSON), checksum, now); err != nil {
		return nil, err
	}

	for _, row := range accessRows {
		var cond string
		if row.Condition != nil {
			raw, err := json.Marshal(row.Condition)
			if err != nil {
				return nil, err
			}
			cond = string(raw)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO atombase_access_policies (definition_id, version, table_name, operation, conditions_json)
			VALUES (?, 1, ?, ?, ?)
		`, defID, row.Table, row.Operation, cond); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return api.getDefinition(ctx, req.Name)
}

func (api *API) pushDefinition(ctx context.Context, name string, req PushDefinitionRequest) (*DefinitionVersion, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	current, err := api.getDefinition(ctx, name)
	if err != nil {
		return nil, err
	}
	var currentSchema Schema
	if err := tools.DecodeSchema(current.Schema, &currentSchema); err != nil {
		return nil, err
	}

	changes := diffSchemas(currentSchema, req.Schema)
	if len(changes) == 0 {
		return nil, tools.ErrNoChanges
	}

	accessRows, err := definitions.ParseAndValidateAccess(current.Type, req.Access, schemaTableSet(req.Schema))
	if err != nil {
		return nil, tools.InvalidRequestErr(err.Error())
	}
	validationResult, err := ValidateMigrationPlan(ctx, req.Schema, nil)
	if err != nil {
		return nil, err
	}
	if !validationResult.Valid {
		return nil, tools.InvalidMigrationErr(validationResult.Errors[0].Message)
	}

	plan, err := GenerateMigrationPlan(currentSchema, req.Schema, changes, req.Merge)
	if err != nil {
		return nil, tools.InvalidMigrationErr(err.Error())
	}
	schemaJSON, err := encodeSchemaForStorage(req.Schema)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(schemaJSON)
	checksum := hex.EncodeToString(hash[:])
	now := time.Now().UTC().Format(time.RFC3339)
	version := current.CurrentVersion + 1

	existingDBs, err := api.getDatabasesByDefinition(ctx, current.ID)
	if err != nil {
		return nil, err
	}
	if len(existingDBs) > 0 && len(plan.SQL) > 0 {
		probeToken, err := api.getDatabaseToken(ctx, existingDBs[0].ID)
		if err != nil {
			return nil, err
		}
		if err := batchExecuteWithTokenFn(ctx, existingDBs[0].ID, probeToken, plan.SQL); err != nil {
			return nil, tools.InvalidMigrationErr(err.Error())
		}
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO atombase_definitions_history (definition_id, version, schema_json, checksum, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, current.ID, version, string(schemaJSON), checksum, now); err != nil {
		return nil, err
	}
	sqlJSON, err := json.Marshal(plan.SQL)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO atombase_migrations (definition_id, from_version, to_version, sql, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, current.ID, current.CurrentVersion, version, string(sqlJSON), now); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM atombase_access_policies
		WHERE definition_id = ? AND version = ?
	`, current.ID, version); err != nil {
		return nil, err
	}
	for _, row := range accessRows {
		var cond string
		if row.Condition != nil {
			raw, err := json.Marshal(row.Condition)
			if err != nil {
				return nil, err
			}
			cond = string(raw)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO atombase_access_policies (definition_id, version, table_name, operation, conditions_json)
			VALUES (?, ?, ?, ?, ?)
		`, current.ID, version, row.Table, row.Operation, cond); err != nil {
			return nil, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE atombase_definitions
		SET current_version = ?, updated_at = ?
		WHERE id = ?
	`, version, now, current.ID); err != nil {
		return nil, err
	}

	if len(existingDBs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE atombase_databases
			SET definition_version = ?, updated_at = ?
			WHERE id = ?
		`, version, now, existingDBs[0].ID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &DefinitionVersion{
		DefinitionID: current.ID,
		Version:      version,
		Schema:       req.Schema,
		Checksum:     checksum,
		CreatedAt:    mustParseTime(now),
	}, nil
}

func (api *API) getDefinitionHistory(ctx context.Context, name string) ([]DefinitionVersion, error) {
	current, err := api.getDefinition(ctx, name)
	if err != nil {
		return nil, err
	}
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT id, definition_id, version, schema_json, checksum, created_at
		FROM atombase_definitions_history
		WHERE definition_id = ?
		ORDER BY version DESC
	`, current.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []DefinitionVersion
	for rows.Next() {
		var item DefinitionVersion
		var schemaJSON string
		var createdAt string
		if err := rows.Scan(&item.ID, &item.DefinitionID, &item.Version, &schemaJSON, &item.Checksum, &createdAt); err != nil {
			return nil, err
		}
		if err := tools.DecodeSchema([]byte(schemaJSON), &item.Schema); err != nil {
			return nil, err
		}
		item.CreatedAt = mustParseTime(createdAt)
		items = append(items, item)
	}
	if items == nil {
		items = []DefinitionVersion{}
	}
	return items, rows.Err()
}

func mustParseTime(raw string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, raw)
	return parsed
}
