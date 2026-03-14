package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/atombasedev/atombase/tools"
)

var (
	ErrMigrationFailed      = errors.New("migration failed")
	ErrDatabaseVersionAhead = errors.New("database version ahead of template version")
	retryBackoff            = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
)

func MigrateIfNeeded(ctx context.Context, dao *TenantConnection) error {
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

	if dao.primaryStore == nil || dao.primaryStore.DB() == nil {
		return errors.New("failed to access primary store: primary store not initialized")
	}

	migrations, err := dao.primaryStore.GetMigrationsBetween(ctx, dao.TemplateID, dao.DatabaseVersion, dao.SchemaVersion)
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
			if err := dao.primaryStore.UpdateDatabaseVersion(ctx, dao.ID, dao.SchemaVersion); err != nil {
				log.Printf("migration version update failed for database_id=%d: %v", dao.ID, err)
			}
			// Update cache with new version
			if dao.Name != "" {
				tools.UpdateDatabaseVersion(dao.Name, dao.SchemaVersion)
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

	dao.primaryStore.RecordMigrationFailure(ctx, dao.ID, dao.DatabaseVersion, dao.SchemaVersion, lastErr)

	return fmt.Errorf("%w: %v", ErrMigrationFailed, lastErr)
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
