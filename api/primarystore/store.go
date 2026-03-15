package primarystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atombasedev/atombase/definitions"
	"github.com/atombasedev/atombase/tools"
)

type DatabaseMeta struct {
	ID                string
	DefinitionID      int32
	DefinitionType    definitions.DefinitionType
	DefinitionVersion int
	AuthToken         string
}

type DefinitionMigration struct {
	ID           int64
	DefinitionID int32
	FromVersion  int
	ToVersion    int
	SQL          []string
	CreatedAt    string
}

type Store struct {
	conn *sql.DB
}

func New(conn *sql.DB) (*Store, error) {
	if conn == nil {
		return nil, errors.New("nil primary database connection")
	}
	return &Store{conn: conn}, nil
}

func (s *Store) Close() error { return nil }

func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.conn
}

func (s *Store) LookupDatabaseByID(id string) (DatabaseMeta, error) {
	if cached, ok := tools.GetDatabase(id); ok {
		return DatabaseMeta{
			ID:                cached.ID,
			DefinitionID:      cached.DefinitionID,
			DefinitionVersion: cached.DatabaseVersion,
			AuthToken:         cached.AuthToken,
		}, nil
	}
	if s == nil || s.conn == nil {
		return DatabaseMeta{}, errors.New("primary store not initialized")
	}

	row := s.conn.QueryRow(`
		SELECT d.id, d.definition_id, def.definition_type, d.definition_version, d.auth_token_encrypted
		FROM atombase_databases d
		JOIN atombase_definitions def ON def.id = d.definition_id
		WHERE d.id = ?
	`, id)

	var meta DatabaseMeta
	var defType string
	var encrypted []byte
	if err := row.Scan(&meta.ID, &meta.DefinitionID, &defType, &meta.DefinitionVersion, &encrypted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DatabaseMeta{}, tools.ErrDatabaseNotFound
		}
		return DatabaseMeta{}, err
	}
	meta.DefinitionType = definitions.DefinitionType(defType)
	if len(encrypted) > 0 {
		token, err := decodeStoredDatabaseToken(encrypted)
		if err != nil {
			return DatabaseMeta{}, err
		}
		meta.AuthToken = token
	}

	tools.SetDatabase(id, tools.CachedDatabase{
		ID:              meta.ID,
		DefinitionID:    meta.DefinitionID,
		DatabaseVersion: meta.DefinitionVersion,
		AuthToken:       meta.AuthToken,
	})
	return meta, nil
}

func (s *Store) LookupDatabaseByName(name string) (DatabaseMeta, error) {
	return s.LookupDatabaseByID(name)
}

func decodeStoredDatabaseToken(storedToken []byte) (string, error) {
	if len(storedToken) == 0 {
		return "", nil
	}
	if !tools.EncryptionEnabled() {
		return string(storedToken), nil
	}
	decrypted, err := tools.Decrypt(storedToken)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt auth token: %w", err)
	}
	return string(decrypted), nil
}

func (s *Store) ResolveDatabaseTarget(ctx context.Context, principal definitions.Principal, header string) (definitions.DatabaseTarget, error) {
	if s == nil || s.conn == nil {
		return definitions.DatabaseTarget{}, errors.New("primary store not initialized")
	}
	parts := strings.SplitN(header, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return definitions.DatabaseTarget{}, tools.InvalidRequestErr("Database header must be formatted as <type>:<name>")
	}
	kind, name := parts[0], parts[1]

	var row *sql.Row
	switch definitions.DefinitionType(kind) {
	case definitions.DefinitionTypeGlobal:
		row = s.conn.QueryRowContext(ctx, `
			SELECT d.id, d.definition_id, def.name, def.definition_type, d.definition_version, d.auth_token_encrypted
			FROM atombase_databases d
			JOIN atombase_definitions def ON def.id = d.definition_id
			WHERE d.id = ? AND def.definition_type = 'global'
		`, name)
	case definitions.DefinitionTypeUser:
		if principal.UserID == "" && !principal.IsService {
			return definitions.DatabaseTarget{}, tools.UnauthorizedErr("user database requires an authenticated session")
		}
		if principal.IsService {
			row = s.conn.QueryRowContext(ctx, `
				SELECT d.id, d.definition_id, def.name, def.definition_type, d.definition_version, d.auth_token_encrypted
				FROM atombase_databases d
				JOIN atombase_definitions def ON def.id = d.definition_id
				WHERE d.id = ? AND def.definition_type = 'user'
			`, name)
		} else {
			row = s.conn.QueryRowContext(ctx, `
				SELECT d.id, d.definition_id, def.name, def.definition_type, d.definition_version, d.auth_token_encrypted
				FROM atombase_users u
				JOIN atombase_databases d ON d.id = u.database_id
				JOIN atombase_definitions def ON def.id = d.definition_id
				WHERE u.id = ? AND def.name = ? AND def.definition_type = 'user'
			`, principal.UserID, name)
		}
	case "org":
		row = s.conn.QueryRowContext(ctx, `
			SELECT d.id, d.definition_id, def.name, def.definition_type, d.definition_version, d.auth_token_encrypted
			FROM atombase_organizations o
			JOIN atombase_databases d ON d.id = o.database_id
			JOIN atombase_definitions def ON def.id = d.definition_id
			WHERE o.id = ? AND def.definition_type = 'organization'
		`, name)
	default:
		return definitions.DatabaseTarget{}, tools.InvalidRequestErr("invalid database type")
	}

	var target definitions.DatabaseTarget
	var defType string
	var encrypted []byte
	if err := row.Scan(&target.DatabaseID, &target.DefinitionID, &target.DefinitionName, &defType, &target.DefinitionVersion, &encrypted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return definitions.DatabaseTarget{}, tools.ErrDatabaseNotFound
		}
		return definitions.DatabaseTarget{}, err
	}
	target.DefinitionType = definitions.DefinitionType(defType)
	token, err := decodeStoredDatabaseToken(encrypted)
	if err != nil {
		return definitions.DatabaseTarget{}, err
	}
	target.AuthToken = token
	return target, nil
}

func (s *Store) LoadAccessPolicy(ctx context.Context, definitionID int32, version int, table, operation string) (*definitions.AccessPolicy, error) {
	if s == nil || s.conn == nil {
		return nil, errors.New("primary store not initialized")
	}
	var raw sql.NullString
	err := s.conn.QueryRowContext(ctx, `
		SELECT conditions_json
		FROM atombase_access_policies
		WHERE definition_id = ? AND version = ? AND table_name = ? AND operation = ?
	`, definitionID, version, table, operation).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	policy := &definitions.AccessPolicy{
		DefinitionID: definitionID,
		Version:      version,
		Table:        table,
		Operation:    operation,
	}
	if raw.Valid && strings.TrimSpace(raw.String) != "" {
		cond, err := definitions.DecodeCondition(raw.String)
		if err != nil {
			return nil, err
		}
		policy.Condition = cond
	}
	return policy, nil
}

func (s *Store) GetDefinitionSchema(ctx context.Context, definitionID int32) (json.RawMessage, int, error) {
	if s == nil || s.conn == nil {
		return nil, 0, errors.New("primary store not initialized")
	}
	row := s.conn.QueryRowContext(ctx, `
		SELECT h.schema_json, d.current_version
		FROM atombase_definitions_history h
		JOIN atombase_definitions d ON d.id = h.definition_id AND h.version = d.current_version
		WHERE h.definition_id = ?
	`, definitionID)
	var schema string
	var version int
	if err := row.Scan(&schema, &version); err != nil {
		return nil, 0, err
	}
	return json.RawMessage(schema), version, nil
}

func (s *Store) GetMigrationsBetween(ctx context.Context, definitionID int32, fromVersion, toVersion int) ([]DefinitionMigration, error) {
	if s == nil || s.conn == nil {
		return nil, errors.New("primary store not initialized")
	}
	rows, err := s.conn.QueryContext(ctx, `
		SELECT id, definition_id, from_version, to_version, sql, created_at
		FROM atombase_migrations
		WHERE definition_id = ? AND from_version >= ? AND to_version <= ?
		ORDER BY from_version ASC
	`, definitionID, fromVersion, toVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var migrations []DefinitionMigration
	for rows.Next() {
		var migration DefinitionMigration
		var sqlJSON string
		if err := rows.Scan(&migration.ID, &migration.DefinitionID, &migration.FromVersion, &migration.ToVersion, &sqlJSON, &migration.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(sqlJSON), &migration.SQL); err != nil {
			return nil, fmt.Errorf("failed to decode migration %d sql: %w", migration.ID, err)
		}
		migrations = append(migrations, migration)
	}
	expected := fromVersion
	for _, migration := range migrations {
		if migration.FromVersion != expected {
			return nil, fmt.Errorf("missing migration step from version %d", expected)
		}
		expected = migration.ToVersion
	}
	if expected != toVersion {
		return nil, fmt.Errorf("missing migrations to reach version %d", toVersion)
	}
	return migrations, rows.Err()
}

func (s *Store) UpdateDatabaseVersion(ctx context.Context, databaseID string, version int) error {
	if s == nil || s.conn == nil {
		return errors.New("primary store not initialized")
	}
	_, err := s.conn.ExecContext(ctx, `
		UPDATE atombase_databases
		SET definition_version = ?, updated_at = ?
		WHERE id = ?
	`, version, time.Now().UTC().Format(time.RFC3339), databaseID)
	return err
}

func (s *Store) RecordMigrationFailure(ctx context.Context, databaseID string, fromVersion, toVersion int, migrationErr error) {
	if s == nil || s.conn == nil || migrationErr == nil {
		return
	}
	_, _ = s.conn.ExecContext(ctx, `
		INSERT INTO atombase_migration_failures (database_id, from_version, to_version, error, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(database_id) DO UPDATE SET
			from_version = excluded.from_version,
			to_version = excluded.to_version,
			error = excluded.error,
			created_at = excluded.created_at
	`, databaseID, fromVersion, toVersion, migrationErr.Error(), time.Now().UTC().Format(time.RFC3339))
}
