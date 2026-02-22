package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

var (
	ErrMigrationFailed      = errors.New("migration failed")
	ErrDatabaseVersionAhead = errors.New("database version ahead of template version")
	retryBackoff            = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
)

type templateMigration struct {
	ID          int64
	TemplateID  int32
	FromVersion int
	ToVersion   int
	SQL         []string
	CreatedAt   string
}

func MigrateIfNeeded(ctx context.Context, dao *Database) error {
	if dao.TemplateID == 0 {
		return nil
	}

	if dao.DatabaseVersion == dao.SchemaVersion {
		return nil
	}

	if dao.DatabaseVersion > dao.SchemaVersion {
		return fmt.Errorf("%w: database_id=%d database_version=%d template_version=%d",
			ErrDatabaseVersionAhead, dao.ID, dao.DatabaseVersion, dao.SchemaVersion)
	}

	migrations, err := getMigrationsBetween(ctx, dao.TemplateID, dao.DatabaseVersion, dao.SchemaVersion)
	if err != nil {
		return fmt.Errorf("failed to load migrations: %w", err)
	}

	var allSQL []string
	for _, migration := range migrations {
		allSQL = append(allSQL, migration.SQL...)
	}

	var lastErr error
	for attempt := 0; attempt < len(retryBackoff); attempt++ {
		if attempt > 0 {
			time.Sleep(retryBackoff[attempt-1])
		}

		execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = executeMigrationBatch(execCtx, dao.Client, allSQL)
		cancel()

		if err == nil {
			if err := updateDatabaseVersion(ctx, dao.ID, dao.SchemaVersion); err != nil {
				log.Printf("migration version update failed for database_id=%d: %v", dao.ID, err)
			}
			dao.DatabaseVersion = dao.SchemaVersion
			return nil
		}

		lastErr = err
		if !isRetryableMigrationError(err) {
			break
		}
	}

	log.Printf("CRITICAL: lazy migration failed database_id=%d template_id=%d from=%d to=%d err=%v",
		dao.ID, dao.TemplateID, dao.DatabaseVersion, dao.SchemaVersion, lastErr)

	recordMigrationFailure(ctx, dao.ID, dao.DatabaseVersion, dao.SchemaVersion, lastErr)

	return fmt.Errorf("%w: %v", ErrMigrationFailed, lastErr)
}

func getMigrationsBetween(ctx context.Context, templateID int32, fromVersion, toVersion int) ([]templateMigration, error) {
	primary, err := ConnPrimary()
	if err != nil {
		return nil, err
	}

	rows, err := primary.Client.QueryContext(ctx, `
		SELECT id, template_id, from_version, to_version, sql, created_at
		FROM atomicbase_migrations
		WHERE template_id = ?
		  AND from_version >= ?
		  AND to_version <= ?
		ORDER BY from_version ASC
	`, templateID, fromVersion, toVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var migrations []templateMigration
	for rows.Next() {
		var migration templateMigration
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

func executeMigrationBatch(ctx context.Context, client *sql.DB, statements []string) error {
	if len(statements) == 0 {
		return nil
	}

	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("statement %d failed: %w", i+1, err)
		}
	}

	return tx.Commit()
}

func updateDatabaseVersion(ctx context.Context, databaseID int32, version int) error {
	primary, err := ConnPrimary()
	if err != nil {
		return err
	}

	_, err = primary.Client.ExecContext(ctx, `
		UPDATE atomicbase_databases
		SET template_version = ?, updated_at = ?
		WHERE id = ?
	`, version, time.Now().UTC().Format(time.RFC3339), databaseID)
	return err
}

func recordMigrationFailure(ctx context.Context, databaseID int32, fromVersion, toVersion int, migrationErr error) {
	if migrationErr == nil {
		return
	}

	primary, err := ConnPrimary()
	if err != nil {
		return
	}

	_, err = primary.Client.ExecContext(ctx, `
		INSERT INTO atomicbase_migration_failures (database_id, from_version, to_version, error, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(database_id) DO UPDATE SET
			from_version = excluded.from_version,
			to_version = excluded.to_version,
			error = excluded.error,
			created_at = excluded.created_at
	`, databaseID, fromVersion, toVersion, migrationErr.Error(), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		log.Printf("failed to record migration failure for database_id=%d: %v", databaseID, err)
	}
}

func isRetryableMigrationError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "temporary")
}
