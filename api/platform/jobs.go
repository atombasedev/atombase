package platform

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/atomicbase/atomicbase/tools"
)

// Job processing constants.
const (
	BatchSize   = 25 // Number of databases to process concurrently
	MaxRetries  = 5  // Maximum retry attempts for network errors
	BaseBackoff = 100 * time.Millisecond

	// Operation timeouts
	DBOperationTimeout  = 30 * time.Second  // Timeout for local database operations
	ExternalAPITimeout  = 60 * time.Second  // Timeout for external API calls (Turso)
	BatchExecuteTimeout = 120 * time.Second // Timeout for batch SQL execution (per database)
)

// Re-export errors from tools for backward compatibility.
var (
	ErrMigrationNotFound = tools.ErrMigrationNotFound
	ErrMigrationLocked   = tools.ErrAtomicbaseBusy
	ErrFirstDBFailed     = errors.New("first database migration failed")
	ErrJobCancelled      = errors.New("migration job cancelled")
)

// withDBTimeout wraps a context with the standard database operation timeout.
func withDBTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, DBOperationTimeout)
}

// withExternalTimeout wraps a context with the external API timeout.
func withExternalTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ExternalAPITimeout)
}

// checkCancelled returns an error if the context is cancelled.
func checkCancelled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ErrJobCancelled
	default:
		return nil
	}
}

// JobManager manages background migration jobs.
type JobManager struct {
	mu      sync.Mutex
	running map[int32]bool // templateID -> is running
	wg      sync.WaitGroup
}

// NewJobManager creates a new job manager.
func NewJobManager() *JobManager {
	return &JobManager{
		running: make(map[int32]bool),
	}
}

// Global job manager instance.
var jobManager = NewJobManager()

// GetJobManager returns the global job manager.
func GetJobManager() *JobManager {
	return jobManager
}

// IsRunning checks if a migration is running for a template.
func (jm *JobManager) IsRunning(templateID int32) bool {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	return jm.running[templateID]
}

// TryLock attempts to acquire the migration lock for a template.
// Returns true if lock acquired, false if already locked.
func (jm *JobManager) TryLock(templateID int32) bool {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if jm.running[templateID] {
		return false
	}
	jm.running[templateID] = true
	return true
}

// Unlock releases the migration lock for a template.
func (jm *JobManager) Unlock(templateID int32) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	delete(jm.running, templateID)
}

// Wait waits for all running jobs to complete.
func (jm *JobManager) Wait() {
	jm.wg.Wait()
}

// MigrationResult tracks the outcome of a single database migration.
type MigrationResult struct {
	DatabaseID int32
	Success    bool
	Error      string
}

// RunMigrationJob executes a migration job in the background.
// This implements the migration flow from the design doc:
// 1. Migrate first DB synchronously (abort if fails)
// 2. Migrate remaining DBs in batches of 25 concurrently
// 3. Update job status and database versions
func RunMigrationJob(ctx context.Context, jobID int64) {
	jm := GetJobManager()

	// Get migration details (use request context for initial fetch)
	migration, err := GetMigration(ctx, jobID)
	if err != nil {
		log.Printf("[job %d] failed to get migration: %v", jobID, err)
		return
	}

	// Try to acquire lock
	if !jm.TryLock(migration.TemplateID) {
		log.Printf("[job %d] migration already running for template %d", jobID, migration.TemplateID)
		return
	}

	jm.wg.Add(1)
	go func() {
		defer jm.wg.Done()
		defer jm.Unlock(migration.TemplateID)

		// Use background context with cancellation for the job.
		// The job runs independently of HTTP request but can be stopped via context.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runMigrationJobInternal(ctx, migration)
	}()
}

// runMigrationJobInternal contains the actual job execution logic.
func runMigrationJobInternal(ctx context.Context, migration *Migration) {
	jobID := migration.ID

	// Get all pending databases first to know total count
	dbCtx, cancel := withDBTimeout(ctx)
	databases, err := GetPendingDatabases(dbCtx, jobID, migration.TemplateID, migration.ToVersion)
	cancel()
	if err != nil {
		log.Printf("[job %d] failed to get pending databases: %v", jobID, err)
		markJobFailed(ctx, jobID, 0, 0)
		return
	}

	// Mark job as running with total count
	dbCtx, cancel = withDBTimeout(ctx)
	err = StartMigration(dbCtx, jobID, len(databases))
	cancel()
	if err != nil {
		log.Printf("[job %d] failed to start migration: %v", jobID, err)
		return
	}

	// No databases to migrate
	if len(databases) == 0 {
		markJobSuccess(ctx, jobID, 0, 0)
		updateTemplateVersion(ctx, migration.TemplateID, migration.ToVersion)
		return
	}

	// Pre-load all needed migrations into cache
	dbCtx, cancel = withDBTimeout(ctx)
	migrations, err := loadMigrationCache(dbCtx, migration.TemplateID, databases, migration.ToVersion)
	cancel()
	if err != nil {
		log.Printf("[job %d] failed to load migration cache: %v", jobID, err)
		markJobFailed(ctx, jobID, 0, 0)
		return
	}

	// First Database Strategy: migrate first database synchronously
	firstTenant := databases[0]
	databases = databases[1:]

	result := migrateTenant(ctx, firstTenant, migrations, migration.ToVersion)

	dbCtx, cancel = withDBTimeout(ctx)
	err = RecordDatabaseMigration(dbCtx, jobID, firstTenant.ID, statusFromResult(result), result.Error)
	cancel()
	if err != nil {
		log.Printf("[job %d] failed to record first database migration: %v", jobID, err)
	}

	if !result.Success {
		// First DB failed - abort entire migration
		log.Printf("[job %d] first database migration failed: %s", jobID, result.Error)
		markJobFailed(ctx, jobID, 0, 1)
		return
	}

	// Update first database version
	dbCtx, cancel = withDBTimeout(ctx)
	err = BatchUpdateDatabaseVersions(dbCtx, []int32{firstTenant.ID}, migration.ToVersion)
	cancel()
	if err != nil {
		log.Printf("[job %d] failed to update first database version: %v", jobID, err)
	}

	// Process remaining databases in batches
	// Start with 1 completed (first database)
	completedCount := 1
	failedCount := 0

	for i := 0; i < len(databases); i += BatchSize {
		// Check for cancellation before processing each batch
		if err := checkCancelled(ctx); err != nil {
			log.Printf("[job %d] job cancelled, stopping at batch %d", jobID, i/BatchSize)
			break
		}

		end := i + BatchSize
		if end > len(databases) {
			end = len(databases)
		}
		batch := databases[i:end]

		// Migrate batch concurrently
		results := migrateBatchConcurrent(ctx, batch, migrations, migration.ToVersion)

		// Collect successful database IDs
		var successIDs []int32
		for _, r := range results {
			if r.Success {
				successIDs = append(successIDs, r.DatabaseID)
				completedCount++
			} else {
				failedCount++
			}

			// Record each database's result
			dbCtx, cancel = withDBTimeout(ctx)
			err = RecordDatabaseMigration(dbCtx, jobID, r.DatabaseID, statusFromResult(r), r.Error)
			cancel()
			if err != nil {
				log.Printf("[job %d] failed to record database %d migration: %v", jobID, r.DatabaseID, err)
			}
		}

		// Batch update successful databases
		if len(successIDs) > 0 {
			dbCtx, cancel = withDBTimeout(ctx)
			err = BatchUpdateDatabaseVersions(dbCtx, successIDs, migration.ToVersion)
			cancel()
			if err != nil {
				log.Printf("[job %d] failed to batch update versions: %v", jobID, err)
			}
		}
	}

	// Determine final state
	if failedCount == 0 {
		markJobSuccess(ctx, jobID, completedCount, failedCount)
	} else if completedCount > 0 {
		markJobPartial(ctx, jobID, completedCount, failedCount)
	} else {
		markJobFailed(ctx, jobID, completedCount, failedCount)
	}

	// Update template version if any succeeded
	if completedCount > 0 {
		updateTemplateVersion(ctx, migration.TemplateID, migration.ToVersion)
	}
}

// loadMigrationCache pre-loads all needed migrations for the job.
func loadMigrationCache(ctx context.Context, templateID int32, databases []Database, targetVersion int) (map[int][]string, error) {
	// Find minimum database version
	minVersion := targetVersion
	for _, t := range databases {
		if t.TemplateVersion < minVersion {
			minVersion = t.TemplateVersion
		}
	}

	// Load all needed migrations
	cache := make(map[int][]string)
	for v := minVersion; v < targetVersion; v++ {
		migration, err := GetMigrationByVersions(ctx, templateID, v, v+1)
		if err != nil {
			return nil, fmt.Errorf("missing migration v%d->v%d: %w", v, v+1, err)
		}
		cache[v] = migration.SQL
	}

	return cache, nil
}

// migrateTenant migrates a single database with retry logic.
func migrateTenant(ctx context.Context, database Database, migrations map[int][]string, targetVersion int) MigrationResult {
	result := MigrationResult{DatabaseID: database.ID}

	// Build chained SQL for this database
	var allSQL []string
	for v := database.TemplateVersion; v < targetVersion; v++ {
		sql, ok := migrations[v]
		if !ok {
			result.Error = fmt.Sprintf("missing migration v%d->v%d", v, v+1)
			return result
		}
		allSQL = append(allSQL, sql...)
	}

	// Nothing to do if already at target
	if len(allSQL) == 0 {
		result.Success = true
		return result
	}

	// Execute with retry
	var lastErr error
	for attempt := 0; attempt < MaxRetries; attempt++ {
		// Check for cancellation before each retry attempt
		if err := checkCancelled(ctx); err != nil {
			result.Error = err.Error()
			return result
		}

		if attempt > 0 {
			backoff := BaseBackoff * time.Duration(1<<uint(attempt-1))
			// Use a timer with context for cancellable sleep
			select {
			case <-ctx.Done():
				result.Error = ErrJobCancelled.Error()
				return result
			case <-time.After(backoff):
			}
		}

		// Execute with timeout
		execCtx, cancel := context.WithTimeout(ctx, BatchExecuteTimeout)
		err := BatchExecute(execCtx, database.Name, allSQL)
		cancel()

		if err == nil {
			result.Success = true
			return result
		}

		lastErr = err

		// Only retry network errors
		if !isRetryableError(err) {
			break
		}
	}

	result.Error = lastErr.Error()
	return result
}

// migrateBatchConcurrent migrates a batch of databases concurrently.
func migrateBatchConcurrent(ctx context.Context, databases []Database, migrations map[int][]string, targetVersion int) []MigrationResult {
	results := make([]MigrationResult, len(databases))

	// Check for cancellation before starting batch
	if err := checkCancelled(ctx); err != nil {
		for i := range results {
			results[i] = MigrationResult{
				DatabaseID: databases[i].ID,
				Success:    false,
				Error:      err.Error(),
			}
		}
		return results
	}

	var wg sync.WaitGroup

	for i, database := range databases {
		wg.Add(1)
		go func(idx int, t Database) {
			defer wg.Done()
			results[idx] = migrateTenant(ctx, t, migrations, targetVersion)
		}(i, database)
	}

	wg.Wait()
	return results
}

// isRetryableError checks if an error is a network error worth retrying.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for network errors (timeout only, Temporary() is deprecated)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Check for common retryable error messages
	errStr := strings.ToLower(err.Error())
	retryableMessages := []string{
		"connection refused",
		"connection reset",
		"timeout",
		"temporary failure",
		"no such host",
		"i/o timeout",
	}

	for _, msg := range retryableMessages {
		if strings.Contains(errStr, msg) {
			return true
		}
	}

	return false
}

// statusFromResult converts a MigrationResult to status string.
func statusFromResult(r MigrationResult) string {
	if r.Success {
		return DatabaseMigrationStatusSuccess
	}
	return DatabaseMigrationStatusFailed
}

// RetryFailedDatabases retries all failed databases from a migration job.
// Returns the count of retried databases and optionally a new job ID.
func RetryFailedDatabases(ctx context.Context, jobID int64) (*RetryMigrationResponse, error) {
	// Get the original migration
	dbCtx, cancel := withDBTimeout(ctx)
	migration, err := GetMigration(dbCtx, jobID)
	cancel()
	if err != nil {
		return nil, err
	}

	// Check if another migration is running
	jm := GetJobManager()
	if jm.IsRunning(migration.TemplateID) {
		return nil, ErrMigrationLocked
	}

	// Get failed databases
	dbCtx, cancel = withDBTimeout(ctx)
	failedTenants, err := GetFailedDatabases(dbCtx, jobID)
	cancel()
	if err != nil {
		return nil, err
	}

	if len(failedTenants) == 0 {
		return &RetryMigrationResponse{
			RetriedCount: 0,
		}, nil
	}

	// Clear failed status so they become pending again
	conn, err := getDB()
	if err != nil {
		return nil, err
	}

	dbCtx, cancel = withDBTimeout(ctx)
	defer cancel()
	for _, database := range failedTenants {
		_, err = conn.ExecContext(dbCtx, fmt.Sprintf(`
			DELETE FROM %s WHERE migration_id = ? AND database_id = ?
		`, TableDatabaseMigrations), jobID, database.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to clear failed status: %w", err)
		}
	}

	// Reset job status to running (keep current counts)
	if err := UpdateMigrationStatus(ctx, jobID, MigrationStatusRunning, nil, migration.CompletedDBs, migration.FailedDBs); err != nil {
		return nil, err
	}

	// Start the job again
	RunMigrationJob(ctx, jobID)

	return &RetryMigrationResponse{
		RetriedCount: len(failedTenants),
	}, nil
}

// ResumeRunningJobs resumes any jobs that were interrupted (e.g., by server restart).
// Should be called during server startup.
func ResumeRunningJobs(ctx context.Context) error {
	conn, err := getDB()
	if err != nil {
		return err
	}

	// Find all running migrations
	dbCtx, cancel := withDBTimeout(ctx)
	defer cancel()

	rows, err := conn.QueryContext(dbCtx, fmt.Sprintf(`
		SELECT id FROM %s WHERE status = ?
	`, TableMigrations), MigrationStatusRunning)
	if err != nil {
		return err
	}
	defer rows.Close()

	var jobIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		jobIDs = append(jobIDs, id)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// Resume each job
	for _, jobID := range jobIDs {
		log.Printf("[startup] resuming interrupted job %d", jobID)
		RunMigrationJob(ctx, jobID)
	}

	if len(jobIDs) > 0 {
		log.Printf("[startup] resumed %d interrupted jobs", len(jobIDs))
	}

	return nil
}

// =============================================================================
// Helper functions for job status updates
// =============================================================================

func markJobSuccess(ctx context.Context, jobID int64, completed, failed int) {
	state := MigrationStateSuccess
	dbCtx, cancel := withDBTimeout(ctx)
	defer cancel()
	if err := UpdateMigrationStatus(dbCtx, jobID, MigrationStatusComplete, &state, completed, failed); err != nil {
		log.Printf("[job %d] failed to mark success: %v", jobID, err)
	}
}

func markJobPartial(ctx context.Context, jobID int64, completed, failed int) {
	state := MigrationStatePartial
	dbCtx, cancel := withDBTimeout(ctx)
	defer cancel()
	if err := UpdateMigrationStatus(dbCtx, jobID, MigrationStatusComplete, &state, completed, failed); err != nil {
		log.Printf("[job %d] failed to mark partial: %v", jobID, err)
	}
}

func markJobFailed(ctx context.Context, jobID int64, completed, failed int) {
	state := MigrationStateFailed
	dbCtx, cancel := withDBTimeout(ctx)
	defer cancel()
	if err := UpdateMigrationStatus(dbCtx, jobID, MigrationStatusComplete, &state, completed, failed); err != nil {
		log.Printf("[job %d] failed to mark failed: %v", jobID, err)
	}
}

func updateTemplateVersion(ctx context.Context, templateID int32, version int) {
	conn, err := getDB()
	if err != nil {
		return
	}

	dbCtx, cancel := withDBTimeout(ctx)
	defer cancel()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = conn.ExecContext(dbCtx, fmt.Sprintf(`
		UPDATE %s SET current_version = ?, updated_at = ? WHERE id = ?
	`, TableTemplates), version, now, templateID)
	if err != nil {
		log.Printf("[template %d] failed to update version to %d: %v", templateID, version, err)
		return
	}

	// Invalidate schema cache so next request loads the new version
	tools.InvalidateTemplate(templateID)
}
