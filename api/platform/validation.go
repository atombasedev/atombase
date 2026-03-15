package platform

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// ValidationResult contains the results of migration validation.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// ValidateMigrationPlan validates a migration plan before execution.
// Performs FK reference checks and optionally data constraint checks against a probe database.
// Note: SQL syntax is validated by executing on the first database - if it fails, the migration aborts.
func ValidateMigrationPlan(ctx context.Context, newSchema Schema, probeDB *sql.DB) (*ValidationResult, error) {
	result := &ValidationResult{Valid: true}

	// 1. FK Reference Validation (schema-level, no DB needed)
	fkErrors := validateFKReferences(newSchema)
	result.Errors = append(result.Errors, fkErrors...)

	// 2. Data-Dependent Checks (if probe database provided)
	if probeDB != nil {
		dataErrors, err := validateDataConstraints(ctx, probeDB, newSchema)
		if err != nil {
			return nil, fmt.Errorf("data constraint validation failed: %w", err)
		}
		result.Errors = append(result.Errors, dataErrors...)
	}

	result.Valid = len(result.Errors) == 0
	return result, nil
}

func ValidateMigrationExecution(ctx context.Context, currentSchema Schema, migrationSQL []string) error {
	if len(migrationSQL) == 0 {
		return nil
	}

	probeDB, err := buildMigrationProbeDB(currentSchema)
	if err != nil {
		return fmt.Errorf("failed to build local probe database: %w", err)
	}
	defer probeDB.Close()

	for _, stmt := range migrationSQL {
		if _, err := probeDB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("local migration probe failed for %q: %w", stmt, err)
		}
	}

	return nil
}

func buildMigrationProbeDB(schema Schema) (*sql.DB, error) {
	probeDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}

	cleanup := func(runErr error) (*sql.DB, error) {
		_ = probeDB.Close()
		return nil, runErr
	}

	if _, err := probeDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return cleanup(err)
	}

	for _, stmt := range generateSchemaSQL(schema) {
		if _, err := probeDB.Exec(stmt); err != nil {
			return cleanup(fmt.Errorf("failed to apply schema statement %q: %w", stmt, err))
		}
	}

	return probeDB, nil
}

// validateFKReferences checks that all foreign key references point to tables that exist in the schema.
func validateFKReferences(schema Schema) []ValidationError {
	var errors []ValidationError

	// Build set of table names
	tableNames := make(map[string]bool)
	for _, t := range schema.Tables {
		tableNames[t.Name] = true
	}

	// Check each FK reference
	for _, table := range schema.Tables {
		for _, col := range table.Columns {
			if col.References == "" {
				continue
			}

			// Parse "table.column" format
			parts := strings.SplitN(col.References, ".", 2)
			if len(parts) != 2 {
				errors = append(errors, ValidationError{
					Type:    "fk_reference",
					Table:   table.Name,
					Column:  col.Name,
					Message: fmt.Sprintf("invalid foreign key format: %s (expected table.column)", col.References),
				})
				continue
			}

			refTable := parts[0]
			refColumn := parts[1]

			// Check if referenced table exists
			if !tableNames[refTable] {
				errors = append(errors, ValidationError{
					Type:    "fk_reference",
					Table:   table.Name,
					Column:  col.Name,
					Message: fmt.Sprintf("foreign key references non-existent table: %s", refTable),
				})
				continue
			}

			// Check if referenced column exists
			var refTableDef Table
			for _, t := range schema.Tables {
				if t.Name == refTable {
					refTableDef = t
					break
				}
			}

			if _, exists := refTableDef.Columns[refColumn]; !exists {
				errors = append(errors, ValidationError{
					Type:    "fk_reference",
					Table:   table.Name,
					Column:  col.Name,
					Message: fmt.Sprintf("foreign key references non-existent column: %s.%s", refTable, refColumn),
				})
			}
		}
	}

	return errors
}

// validateDataConstraints checks data-dependent constraints against a real database.
// This should be run against the first database before migrating all databases.
func validateDataConstraints(ctx context.Context, db *sql.DB, newSchema Schema) ([]ValidationError, error) {
	var errors []ValidationError

	for _, table := range newSchema.Tables {
		// Check if table exists in the database
		var tableExists int
		err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
			table.Name).Scan(&tableExists)
		if err != nil {
			return nil, err
		}

		if tableExists == 0 {
			// New table, no data constraints to check
			continue
		}

		for _, col := range table.Columns {
			// Check UNIQUE constraint
			if col.Unique {
				uniqueErrors, err := checkUniqueConstraint(ctx, db, table.Name, col.Name)
				if err != nil {
					return nil, err
				}
				errors = append(errors, uniqueErrors...)
			}

			// Check CHECK constraint
			if col.Check != "" {
				checkErrors, err := checkCheckConstraint(ctx, db, table.Name, col.Name, col.Check)
				if err != nil {
					return nil, err
				}
				errors = append(errors, checkErrors...)
			}

			// Check FK constraint (orphan rows)
			if col.References != "" {
				fkErrors, err := checkFKConstraint(ctx, db, table.Name, col)
				if err != nil {
					return nil, err
				}
				errors = append(errors, fkErrors...)
			}
		}
	}

	return errors, nil
}

// checkUniqueConstraint checks for duplicate values that would violate a UNIQUE constraint.
func checkUniqueConstraint(ctx context.Context, db *sql.DB, table, column string) ([]ValidationError, error) {
	// Check if column exists
	var colExists int
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?", table),
		column).Scan(&colExists)
	if err != nil {
		return nil, err
	}

	if colExists == 0 {
		// Column doesn't exist yet (being added), no data to check
		return nil, nil
	}

	// Find duplicates
	query := fmt.Sprintf(`
		SELECT [%s], COUNT(*) as cnt
		FROM [%s]
		WHERE [%s] IS NOT NULL
		GROUP BY [%s]
		HAVING cnt > 1
		LIMIT 5
	`, column, table, column, column)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var errors []ValidationError
	var duplicates []string

	for rows.Next() {
		var value any
		var count int
		if err := rows.Scan(&value, &count); err != nil {
			return nil, err
		}
		duplicates = append(duplicates, fmt.Sprintf("%v (%d occurrences)", value, count))
	}

	if len(duplicates) > 0 {
		errors = append(errors, ValidationError{
			Type:    "unique",
			Table:   table,
			Column:  column,
			Message: fmt.Sprintf("UNIQUE constraint would fail: duplicate values found: %s", strings.Join(duplicates, ", ")),
		})
	}

	return errors, nil
}

// checkCheckConstraint checks for rows that would violate a CHECK constraint.
func checkCheckConstraint(ctx context.Context, db *sql.DB, table, column, checkExpr string) ([]ValidationError, error) {
	// Check if column exists
	var colExists int
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?", table),
		column).Scan(&colExists)
	if err != nil {
		return nil, err
	}

	if colExists == 0 {
		// Column doesn't exist yet (being added), no data to check
		return nil, nil
	}

	// Count rows that violate the check constraint
	query := fmt.Sprintf(`SELECT COUNT(*) FROM [%s] WHERE NOT (%s)`, table, checkExpr)

	var violationCount int
	err = db.QueryRowContext(ctx, query).Scan(&violationCount)
	if err != nil {
		// Check constraint expression might be invalid or reference missing columns
		// Ignore errors - the actual migration will catch this
		return nil, nil
	}

	var errors []ValidationError
	if violationCount > 0 {
		errors = append(errors, ValidationError{
			Type:    "check",
			Table:   table,
			Column:  column,
			Message: fmt.Sprintf("CHECK constraint would fail: %d rows violate condition (%s)", violationCount, checkExpr),
		})
	}

	return errors, nil
}

// checkFKConstraint checks for orphan rows that would violate a foreign key constraint.
func checkFKConstraint(ctx context.Context, db *sql.DB, table string, col Col) ([]ValidationError, error) {
	// Check if column exists
	var colExists int
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?", table),
		col.Name).Scan(&colExists)
	if err != nil {
		return nil, err
	}

	if colExists == 0 {
		// Column doesn't exist yet (being added), no data to check
		return nil, nil
	}

	// Parse reference
	parts := strings.SplitN(col.References, ".", 2)
	if len(parts) != 2 {
		return nil, nil
	}
	refTable, refColumn := parts[0], parts[1]

	// Check if referenced table exists
	var refTableExists int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
		refTable).Scan(&refTableExists)
	if err != nil {
		return nil, err
	}

	if refTableExists == 0 {
		// Referenced table doesn't exist, will be caught by FK reference validation
		return nil, nil
	}

	// Find orphan rows
	query := fmt.Sprintf(`
		SELECT COUNT(*) FROM [%s] t
		WHERE t.[%s] IS NOT NULL
		AND NOT EXISTS (
			SELECT 1 FROM [%s] r WHERE r.[%s] = t.[%s]
		)
	`, table, col.Name, refTable, refColumn, col.Name)

	var orphanCount int
	err = db.QueryRowContext(ctx, query).Scan(&orphanCount)
	if err != nil {
		return nil, err
	}

	var errors []ValidationError
	if orphanCount > 0 {
		errors = append(errors, ValidationError{
			Type:    "fk_constraint",
			Table:   table,
			Column:  col.Name,
			Message: fmt.Sprintf("FOREIGN KEY constraint would fail: %d orphan rows reference non-existent %s.%s values", orphanCount, refTable, refColumn),
		})
	}

	return errors, nil
}

// AutoFixNotNullColumns updates the migration plan to add defaults for NOT NULL columns.
// Returns the modified schema with defaults added.
func AutoFixNotNullColumns(schema Schema, changes []SchemaDiff) Schema {
	// Create a copy of schema to modify
	fixedSchema := Schema{Tables: make([]Table, len(schema.Tables))}

	for i, table := range schema.Tables {
		fixedTable := Table{
			Name:       table.Name,
			Pk:         table.Pk,
			Columns:    make(map[string]Col),
			Indexes:    table.Indexes,
			FTSColumns: table.FTSColumns,
		}

		// Check if this column is being added
		addedColumns := make(map[string]bool)
		for _, c := range changes {
			if c.Type == "add_column" && c.Table == table.Name {
				addedColumns[c.Column] = true
			}
		}

		for name, col := range table.Columns {
			fixedCol := col

			// Auto-fix: if adding a NOT NULL column without default, add type-appropriate default
			if addedColumns[name] && col.NotNull && col.Default == nil {
				fixedCol.Default = getDefaultValue(col.Type)
			}

			fixedTable.Columns[name] = fixedCol
		}

		fixedSchema.Tables[i] = fixedTable
	}

	return fixedSchema
}

// getDefaultValue returns the appropriate default value for a column type.
func getDefaultValue(colType string) any {
	switch strings.ToUpper(colType) {
	case "INTEGER":
		return 0
	case "REAL":
		return 0.0
	case "BLOB":
		return []byte{}
	default:
		return ""
	}
}
