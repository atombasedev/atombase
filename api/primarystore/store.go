package primarystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atomicbase/atomicbase/tools"
)

// DatabaseMeta is internal metadata for a provisioned external database.
type DatabaseMeta struct {
	ID              int32
	TemplateID      int32
	DatabaseVersion int
}

// TemplateMigration is a migration step for a template version range.
type TemplateMigration struct {
	ID          int64
	TemplateID  int32
	FromVersion int
	ToVersion   int
	SQL         []string
	CreatedAt   string
}

// Store provides internal-only access to the primary metadata database.
type Store struct {
	conn       *sql.DB
	dbLookupBy *sql.Stmt
}

// New creates a primary store from a shared primary database connection.
func New(conn *sql.DB) (*Store, error) {
	if conn == nil {
		return nil, errors.New("nil primary database connection")
	}

	lookupStmt, err := conn.Prepare(fmt.Sprintf(
		"SELECT id, COALESCE(template_id, 0), COALESCE(template_version, 1) FROM %s WHERE name = ?",
		"atomicbase_databases",
	))
	if err != nil {
		return nil, fmt.Errorf("failed to prepare database lookup statement: %w", err)
	}

	return &Store{conn: conn, dbLookupBy: lookupStmt}, nil
}

// Close releases prepared resources owned by the store.
// The caller that owns the shared primary connection remains responsible for conn.Close().
func (s *Store) Close() error {
	if s == nil || s.dbLookupBy == nil {
		return nil
	}
	err := s.dbLookupBy.Close()
	s.dbLookupBy = nil
	return err
}

// DB returns the shared primary database connection.
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.conn
}

// LookupDatabaseByName fetches internal metadata for a named external database.
func (s *Store) LookupDatabaseByName(name string) (DatabaseMeta, error) {
	if s == nil || s.dbLookupBy == nil {
		return DatabaseMeta{}, errors.New("primary store not initialized")
	}

	row := s.dbLookupBy.QueryRow(name)

	var id sql.NullInt32
	var templateID int32
	var databaseVersion int

	err := row.Scan(&id, &templateID, &databaseVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DatabaseMeta{}, tools.ErrDatabaseNotFound
		}
		return DatabaseMeta{}, err
	}

	return DatabaseMeta{
		ID:              id.Int32,
		TemplateID:      templateID,
		DatabaseVersion: databaseVersion,
	}, nil
}

// GetMigrationsBetween fetches contiguous migrations from [fromVersion, toVersion].
func (s *Store) GetMigrationsBetween(ctx context.Context, templateID int32, fromVersion, toVersion int) ([]TemplateMigration, error) {
	if s == nil || s.conn == nil {
		return nil, errors.New("primary store not initialized")
	}

	rows, err := s.conn.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, template_id, from_version, to_version, sql, created_at
		FROM %s
		WHERE template_id = ?
		  AND from_version >= ?
		  AND to_version <= ?
		ORDER BY from_version ASC
	`, "atomicbase_migrations"), templateID, fromVersion, toVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var migrations []TemplateMigration
	for rows.Next() {
		var migration TemplateMigration
		var sqlJSON string
		if err := rows.Scan(
			&migration.ID,
			&migration.TemplateID,
			&migration.FromVersion,
			&migration.ToVersion,
			&sqlJSON,
			&migration.CreatedAt,
		); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(sqlJSON), &migration.SQL); err != nil {
			return nil, fmt.Errorf("failed to decode migration %d SQL: %w", migration.ID, err)
		}
		migrations = append(migrations, migration)
	}

	if err := rows.Err(); err != nil {
		return nil, err
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

	return migrations, nil
}

// UpdateDatabaseVersion updates atomicbase_databases.template_version.
func (s *Store) UpdateDatabaseVersion(ctx context.Context, databaseID int32, version int) error {
	if s == nil || s.conn == nil {
		return errors.New("primary store not initialized")
	}

	_, err := s.conn.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET template_version = ?, updated_at = ?
		WHERE id = ?
	`, "atomicbase_databases"), version, time.Now().UTC().Format(time.RFC3339), databaseID)
	return err
}

// RecordMigrationFailure stores the last failed migration attempt for a database.
func (s *Store) RecordMigrationFailure(ctx context.Context, databaseID int32, fromVersion, toVersion int, migrationErr error) {
	if s == nil || s.conn == nil || migrationErr == nil {
		return
	}

	_, _ = s.conn.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (database_id, from_version, to_version, error, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(database_id) DO UPDATE SET
			from_version = excluded.from_version,
			to_version = excluded.to_version,
			error = excluded.error,
			created_at = excluded.created_at
	`, "atomicbase_migration_failures"), databaseID, fromVersion, toVersion, migrationErr.Error(), time.Now().UTC().Format(time.RFC3339))
}
