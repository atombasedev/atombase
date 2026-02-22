package platform

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atomicbase/atomicbase/tools"
)

// GenerateMigrationPlan creates a migration plan from schema diff and merges.
// Merges convert drop+add pairs into renames by index in the changes array.
func GenerateMigrationPlan(oldSchema, newSchema Schema, changes []SchemaDiff, merges []Merge) (*MigrationPlan, error) {
	// Apply merges to convert drop+add pairs to renames
	renames := applyMerges(changes, merges)

	// Build sets of renamed items to skip in add/drop processing
	renamedTables := make(map[string]string)  // old name -> new name
	renamedColumns := make(map[string]string) // "table.old" -> new name
	for _, r := range renames {
		if r.Type == "rename_table" {
			renamedTables[r.OldName] = r.NewName
		} else if r.Type == "rename_column" {
			key := r.Table + "." + r.OldName
			renamedColumns[key] = r.NewName
		}
	}

	var statements []string

	// 1. Renames first (so new names are available for subsequent statements)
	for _, r := range renames {
		if r.Type == "rename_table" {
			statements = append(statements, fmt.Sprintf(
				"ALTER TABLE [%s] RENAME TO [%s]", r.OldName, r.NewName))
		} else if r.Type == "rename_column" {
			statements = append(statements, fmt.Sprintf(
				"ALTER TABLE [%s] RENAME COLUMN [%s] TO [%s]", r.Table, r.OldName, r.NewName))
		}
	}

	// Categorize remaining changes (excluding merged ones)
	var addTables, dropTables []SchemaDiff
	var addColumns, dropColumns, modifyColumns []SchemaDiff
	var addIndexes, dropIndexes []SchemaDiff
	var addFTS, dropFTS []SchemaDiff
	var pkTypeChanges []SchemaDiff

	mergedIndices := getMergedIndices(merges)

	for i, c := range changes {
		// Skip merged changes
		if mergedIndices[i] {
			continue
		}

		switch c.Type {
		case "add_table":
			addTables = append(addTables, c)
		case "drop_table":
			if _, renamed := renamedTables[c.Table]; !renamed {
				dropTables = append(dropTables, c)
			}
		case "add_column":
			key := c.Table + "." + c.Column
			if _, renamed := renamedColumns[key]; !renamed {
				addColumns = append(addColumns, c)
			}
		case "drop_column":
			key := c.Table + "." + c.Column
			if _, renamed := renamedColumns[key]; !renamed {
				dropColumns = append(dropColumns, c)
			}
		case "modify_column":
			modifyColumns = append(modifyColumns, c)
		case "add_index":
			addIndexes = append(addIndexes, c)
		case "drop_index":
			dropIndexes = append(dropIndexes, c)
		case "add_fts":
			addFTS = append(addFTS, c)
		case "drop_fts":
			dropFTS = append(dropFTS, c)
		case "change_pk_type":
			pkTypeChanges = append(pkTypeChanges, c)
		}
	}

	// Build lookup maps for schemas
	oldTables := make(map[string]Table)
	for _, t := range oldSchema.Tables {
		oldTables[t.Name] = t
	}
	newTables := make(map[string]Table)
	for _, t := range newSchema.Tables {
		newTables[t.Name] = t
	}

	// 2. Adds (dependency order: tables, columns, indexes, FTS)

	// Add tables
	for _, c := range addTables {
		if table, ok := newTables[c.Table]; ok {
			sql := generateCreateTableSQL(table)
			statements = append(statements, sql)
		}
	}

	// Add columns (check if mirror table needed)
	for _, c := range addColumns {
		table := newTables[c.Table]
		col := table.Columns[c.Column]
		if requiresMirrorTable(Col{}, col) {
			// Need mirror table for this change
			mirrorSQL := generateMirrorTableSQL(oldTables[c.Table], table)
			statements = append(statements, mirrorSQL...)
		} else {
			sql := generateAddColumnSQL(c.Table, col)
			statements = append(statements, sql)
		}
	}

	// Modify columns (most require mirror table)
	for _, c := range modifyColumns {
		oldTable := oldTables[c.Table]
		newTable := newTables[c.Table]
		oldCol := oldTable.Columns[c.Column]
		newCol := newTable.Columns[c.Column]

		if requiresMirrorTable(oldCol, newCol) {
			mirrorSQL := generateMirrorTableSQL(oldTable, newTable)
			statements = append(statements, mirrorSQL...)
		}
		// Type-only changes are metadata-only, no SQL needed
	}

	// PK type changes (always need mirror table)
	for _, c := range pkTypeChanges {
		oldTable := oldTables[c.Table]
		newTable := newTables[c.Table]
		mirrorSQL := generateMirrorTableSQL(oldTable, newTable)
		statements = append(statements, mirrorSQL...)
	}

	// Add indexes
	for _, c := range addIndexes {
		table := newTables[c.Table]
		for _, idx := range table.Indexes {
			if idx.Name == c.Column { // Column field holds index name
				sql := generateCreateIndexSQL(c.Table, idx)
				statements = append(statements, sql)
				break
			}
		}
	}

	// Add FTS
	for _, c := range addFTS {
		table := newTables[c.Table]
		if len(table.FTSColumns) > 0 {
			ftsSQL := generateFTSSQL(c.Table, table.FTSColumns, table.Pk)
			statements = append(statements, ftsSQL...)
		}
	}

	// 3. Drops (reverse dependency order: FTS, indexes, columns, tables)

	// Drop FTS
	for _, c := range dropFTS {
		ftsSQL := generateDropFTSSQL(c.Table)
		statements = append(statements, ftsSQL...)
	}

	// Drop indexes
	for _, c := range dropIndexes {
		statements = append(statements, fmt.Sprintf("DROP INDEX IF EXISTS [%s]", c.Column))
	}

	// Drop columns
	for _, c := range dropColumns {
		statements = append(statements, fmt.Sprintf(
			"ALTER TABLE [%s] DROP COLUMN [%s]", c.Table, c.Column))
	}

	// Drop tables
	for _, c := range dropTables {
		statements = append(statements, fmt.Sprintf("DROP TABLE IF EXISTS [%s]", c.Table))
	}

	return &MigrationPlan{SQL: statements}, nil
}

// rename holds information about a rename operation.
type rename struct {
	Type    string // rename_table or rename_column
	Table   string // table name (for column renames)
	OldName string
	NewName string
}

// applyMerges converts drop+add pairs to renames based on merge indices.
func applyMerges(changes []SchemaDiff, merges []Merge) []rename {
	var renames []rename

	for _, m := range merges {
		if m.Old < 0 || m.Old >= len(changes) || m.New < 0 || m.New >= len(changes) {
			continue
		}

		dropChange := changes[m.Old]
		addChange := changes[m.New]

		// Table rename: drop_table + add_table
		if dropChange.Type == "drop_table" && addChange.Type == "add_table" {
			renames = append(renames, rename{
				Type:    "rename_table",
				OldName: dropChange.Table,
				NewName: addChange.Table,
			})
		}

		// Column rename: drop_column + add_column (same table)
		if dropChange.Type == "drop_column" && addChange.Type == "add_column" &&
			dropChange.Table == addChange.Table {
			renames = append(renames, rename{
				Type:    "rename_column",
				Table:   dropChange.Table,
				OldName: dropChange.Column,
				NewName: addChange.Column,
			})
		}
	}

	return renames
}

// getMergedIndices returns a set of indices that are part of merges.
func getMergedIndices(merges []Merge) map[int]bool {
	indices := make(map[int]bool)
	for _, m := range merges {
		indices[m.Old] = true
		indices[m.New] = true
	}
	return indices
}

// requiresMirrorTable checks if a column modification requires mirror table approach.
func requiresMirrorTable(old, new Col) bool {
	// Adding FK constraint
	if old.References == "" && new.References != "" {
		return true
	}
	// Modifying FK constraint
	if old.References != "" && new.References != "" {
		if old.References != new.References || old.OnDelete != new.OnDelete || old.OnUpdate != new.OnUpdate {
			return true
		}
	}
	// Removing FK constraint
	if old.References != "" && new.References == "" {
		return true
	}

	// CHECK constraint changes
	if old.Check != new.Check {
		return true
	}

	// COLLATE changes
	if old.Collate != new.Collate {
		return true
	}

	// Generated column changes
	if old.Generated == nil && new.Generated != nil {
		return true // regular -> generated
	}
	if old.Generated != nil && new.Generated == nil {
		return true // generated -> regular
	}
	if old.Generated != nil && new.Generated != nil {
		if old.Generated.Expr != new.Generated.Expr || old.Generated.Stored != new.Generated.Stored {
			return true
		}
	}

	return false
}

// generateCreateTableSQL generates CREATE TABLE statement.
// Uses typeless columns except for PK (per design doc).
func generateCreateTableSQL(t Table) string {
	var cols []string
	var fks []string

	// Sort column names for deterministic output
	colNames := make([]string, 0, len(t.Columns))
	for name := range t.Columns {
		colNames = append(colNames, name)
	}
	sort.Strings(colNames)

	for _, name := range colNames {
		col := t.Columns[name]
		colDef := generateColumnDef(col, t.Pk)
		cols = append(cols, colDef)

		// Collect FK constraints
		if col.References != "" {
			fk := generateFKConstraint(col)
			fks = append(fks, fk)
		}
	}

	// Add composite PK if more than one column
	if len(t.Pk) > 1 {
		pkCols := make([]string, len(t.Pk))
		for i, pk := range t.Pk {
			pkCols[i] = "[" + pk + "]"
		}
		cols = append(cols, "PRIMARY KEY ("+strings.Join(pkCols, ", ")+")")
	}

	// Add FK constraints
	cols = append(cols, fks...)

	return fmt.Sprintf("CREATE TABLE [%s] (\n  %s\n)", t.Name, strings.Join(cols, ",\n  "))
}

// generateColumnDef generates a column definition for CREATE TABLE.
func generateColumnDef(col Col, pk []string) string {
	var parts []string
	parts = append(parts, "["+col.Name+"]")

	// Only add type for PK columns (INTEGER PRIMARY KEY for rowid alias)
	isPK := len(pk) == 1 && pk[0] == col.Name
	isCompositePK := false
	for _, p := range pk {
		if p == col.Name {
			isCompositePK = true
			break
		}
	}

	if isPK {
		// Use INTEGER PRIMARY KEY for integer types (enables rowid alias)
		// Use explicit type + PRIMARY KEY for other types (e.g., TEXT)
		colType := strings.ToUpper(col.Type)
		if colType == "INTEGER" || colType == "INT" {
			parts = append(parts, "INTEGER PRIMARY KEY")
		} else {
			parts = append(parts, colType, "PRIMARY KEY")
		}
	} else if isCompositePK && strings.ToUpper(col.Type) == "INTEGER" {
		// Composite PK integer columns get type
		parts = append(parts, "INTEGER")
	}
	// Regular columns: no type (per design doc - typeless columns)

	if col.NotNull && !isPK {
		parts = append(parts, "NOT NULL")
	}

	if col.Unique {
		parts = append(parts, "UNIQUE")
	}

	if col.Default != nil {
		parts = append(parts, "DEFAULT "+formatDefault(col.Default))
	}

	if col.Collate != "" {
		parts = append(parts, "COLLATE "+col.Collate)
	}

	if col.Check != "" {
		parts = append(parts, "CHECK ("+col.Check+")")
	}

	if col.Generated != nil {
		storage := "VIRTUAL"
		if col.Generated.Stored {
			storage = "STORED"
		}
		parts = append(parts, fmt.Sprintf("GENERATED ALWAYS AS (%s) %s", col.Generated.Expr, storage))
	}

	return strings.Join(parts, " ")
}

// generateFKConstraint generates a FOREIGN KEY constraint clause.
func generateFKConstraint(col Col) string {
	// Parse "table.column" format
	parts := strings.SplitN(col.References, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	refTable, refCol := parts[0], parts[1]

	fk := fmt.Sprintf("FOREIGN KEY ([%s]) REFERENCES [%s]([%s])", col.Name, refTable, refCol)

	if col.OnDelete != "" {
		fk += " ON DELETE " + col.OnDelete
	}
	if col.OnUpdate != "" {
		fk += " ON UPDATE " + col.OnUpdate
	}

	return fk
}

// generateAddColumnSQL generates ALTER TABLE ADD COLUMN statement.
func generateAddColumnSQL(table string, col Col) string {
	var parts []string
	parts = append(parts, "["+col.Name+"]")

	// For ADD COLUMN, we don't add type (typeless columns)

	if col.NotNull {
		// NOT NULL requires a default value for existing rows
		def := getDefaultForType(col.Type)
		if col.Default != nil {
			def = formatDefault(col.Default)
		}
		parts = append(parts, "NOT NULL DEFAULT "+def)
	} else if col.Default != nil {
		parts = append(parts, "DEFAULT "+formatDefault(col.Default))
	}

	if col.Unique {
		parts = append(parts, "UNIQUE")
	}

	if col.Check != "" {
		parts = append(parts, "CHECK ("+col.Check+")")
	}

	// Note: Can't add FK via ALTER TABLE, would need mirror table
	// Note: Can't add generated columns via ALTER TABLE

	return fmt.Sprintf("ALTER TABLE [%s] ADD COLUMN %s", table, strings.Join(parts, " "))
}

// generateMirrorTableSQL generates the SQL for mirror table approach.
// Creates new table, copies data, drops old, renames new.
func generateMirrorTableSQL(oldTable, newTable Table) []string {
	tempName := newTable.Name + "_new"

	// Create new table with temp name
	createSQL := generateCreateTableSQL(Table{
		Name:       tempName,
		Pk:         newTable.Pk,
		Columns:    newTable.Columns,
		Indexes:    nil, // Indexes added after data copy
		FTSColumns: nil, // FTS added after
	})

	// Build column mapping for INSERT
	var oldCols, newCols []string
	for colName, newCol := range newTable.Columns {
		if oldCol, exists := oldTable.Columns[colName]; exists {
			oldCols = append(oldCols, "["+colName+"]")
			// Cast if types differ and it's a PK column
			isPK := false
			for _, pk := range newTable.Pk {
				if pk == colName {
					isPK = true
					break
				}
			}
			if isPK && oldCol.Type != newCol.Type {
				newCols = append(newCols, fmt.Sprintf("CAST([%s] AS %s)", colName, newCol.Type))
			} else {
				newCols = append(newCols, "["+colName+"]")
			}
		}
	}

	// Sort for deterministic output
	sort.Strings(oldCols)
	sort.Strings(newCols)

	copySQL := fmt.Sprintf("INSERT INTO [%s] (%s) SELECT %s FROM [%s]",
		tempName, strings.Join(oldCols, ", "), strings.Join(newCols, ", "), oldTable.Name)

	dropSQL := fmt.Sprintf("DROP TABLE [%s]", oldTable.Name)
	renameSQL := fmt.Sprintf("ALTER TABLE [%s] RENAME TO [%s]", tempName, newTable.Name)

	return []string{createSQL, copySQL, dropSQL, renameSQL}
}

// generateCreateIndexSQL generates CREATE INDEX statement.
func generateCreateIndexSQL(table string, idx Index) string {
	cols := make([]string, len(idx.Columns))
	for i, c := range idx.Columns {
		cols[i] = "[" + c + "]"
	}

	unique := ""
	if idx.Unique {
		unique = "UNIQUE "
	}

	return fmt.Sprintf("CREATE %sINDEX IF NOT EXISTS [%s] ON [%s] (%s)",
		unique, idx.Name, table, strings.Join(cols, ", "))
}

// generateFTSSQL generates FTS5 virtual table and triggers.
func generateFTSSQL(table string, ftsColumns []string, pk []string) []string {
	ftsTable := table + "_fts"

	// Build column list for FTS
	cols := make([]string, len(ftsColumns))
	for i, c := range ftsColumns {
		cols[i] = "[" + c + "]"
	}

	// Content table reference
	contentCols := strings.Join(cols, ", ")

	// Create FTS5 virtual table
	createFTS := fmt.Sprintf(
		"CREATE VIRTUAL TABLE IF NOT EXISTS [%s] USING fts5(%s, content=[%s], content_rowid=[%s])",
		ftsTable, contentCols, table, pk[0])

	// Triggers for keeping FTS in sync
	pkCol := pk[0]

	insertTrigger := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS [%s_ai] AFTER INSERT ON [%s] BEGIN
  INSERT INTO [%s]([rowid], %s) VALUES (NEW.[%s], %s);
END`,
		ftsTable, table, ftsTable, contentCols, pkCol,
		prefixColumns(ftsColumns, "NEW."))

	deleteTrigger := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS [%s_ad] AFTER DELETE ON [%s] BEGIN
  INSERT INTO [%s]([%s], [rowid], %s) VALUES ('delete', OLD.[%s], %s);
END`,
		ftsTable, table, ftsTable, ftsTable, contentCols, pkCol,
		prefixColumns(ftsColumns, "OLD."))

	updateTrigger := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS [%s_au] AFTER UPDATE ON [%s] BEGIN
  INSERT INTO [%s]([%s], [rowid], %s) VALUES ('delete', OLD.[%s], %s);
  INSERT INTO [%s]([rowid], %s) VALUES (NEW.[%s], %s);
END`,
		ftsTable, table, ftsTable, ftsTable, contentCols, pkCol,
		prefixColumns(ftsColumns, "OLD."),
		ftsTable, contentCols, pkCol,
		prefixColumns(ftsColumns, "NEW."))

	return []string{createFTS, insertTrigger, deleteTrigger, updateTrigger}
}

// generateDropFTSSQL generates SQL to remove FTS virtual table and triggers.
func generateDropFTSSQL(table string) []string {
	ftsTable := table + "_fts"
	return []string{
		fmt.Sprintf("DROP TRIGGER IF EXISTS [%s_ai]", ftsTable),
		fmt.Sprintf("DROP TRIGGER IF EXISTS [%s_ad]", ftsTable),
		fmt.Sprintf("DROP TRIGGER IF EXISTS [%s_au]", ftsTable),
		fmt.Sprintf("DROP TABLE IF EXISTS [%s]", ftsTable),
	}
}

// prefixColumns returns comma-separated column names with a prefix.
func prefixColumns(cols []string, prefix string) string {
	result := make([]string, len(cols))
	for i, c := range cols {
		result[i] = prefix + "[" + c + "]"
	}
	return strings.Join(result, ", ")
}

// formatDefault formats a default value for SQL.
func formatDefault(val any) string {
	switch v := val.(type) {
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case bool:
		if v {
			return "1"
		}
		return "0"
	case nil:
		return "NULL"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// getDefaultForType returns the default value for a column type.
// Used for auto-fix when adding NOT NULL columns.
func getDefaultForType(colType string) string {
	switch strings.ToUpper(colType) {
	case "INTEGER":
		return "0"
	case "REAL":
		return "0"
	case "BLOB":
		return "X''"
	default:
		return "''"
	}
}

// Execer is an interface for executing SQL statements (satisfied by *sql.DB and *sql.Tx).
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// CreateMigration creates a new migration record in the database.
func CreateMigration(ctx context.Context, templateID int32, fromVersion, toVersion int, sqlStatements []string) (*Migration, error) {
	conn, err := getDB()
	if err != nil {
		return nil, err
	}
	return CreateMigrationTx(ctx, conn, templateID, fromVersion, toVersion, sqlStatements)
}

// CreateMigrationTx creates a new migration record using the provided executor (DB or Tx).
func CreateMigrationTx(ctx context.Context, exec Execer, templateID int32, fromVersion, toVersion int, sqlStatements []string) (*Migration, error) {
	now := time.Now().UTC()

	sqlJSON, err := json.Marshal(sqlStatements)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal SQL: %w", err)
	}

	result, err := exec.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (template_id, from_version, to_version, sql, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, TableMigrations), templateID, fromVersion, toVersion, string(sqlJSON), "ready", now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &Migration{
		ID:          id,
		TemplateID:  templateID,
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		SQL:         sqlStatements,
		Status:      "ready",
		CreatedAt:   now,
	}, nil
}

// GetMigration retrieves a migration by ID.
func GetMigration(ctx context.Context, id int64) (*Migration, error) {
	conn, err := getDB()
	if err != nil {
		return nil, err
	}

	row := conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, template_id, from_version, to_version, sql, status, created_at
		FROM %s WHERE id = ?
	`, TableMigrations), id)

	var m Migration
	var sqlJSON string
	var createdAt sql.NullString

	err = row.Scan(&m.ID, &m.TemplateID, &m.FromVersion, &m.ToVersion, &sqlJSON,
		&m.Status, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrMigrationNotFound
		}
		return nil, err
	}

	if err := json.Unmarshal([]byte(sqlJSON), &m.SQL); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SQL: %w", err)
	}

	if createdAt.Valid {
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
	}

	return &m, nil
}

// ListMigrations retrieves all migrations, optionally filtered by status.
func ListMigrations(ctx context.Context, status string) ([]Migration, error) {
	conn, err := getDB()
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT id, template_id, from_version, to_version, sql, status, created_at
		FROM %s
	`, TableMigrations)

	var args []any
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY id DESC"

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var migrations []Migration
	for rows.Next() {
		var m Migration
		var sqlJSON string
		var createdAt sql.NullString

		err = rows.Scan(&m.ID, &m.TemplateID, &m.FromVersion, &m.ToVersion, &sqlJSON,
			&m.Status, &createdAt)
		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(sqlJSON), &m.SQL); err != nil {
			return nil, fmt.Errorf("failed to unmarshal SQL: %w", err)
		}

		if createdAt.Valid {
			m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
		}

		migrations = append(migrations, m)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return migrations, nil
}

// UpdateMigrationStatus updates the status and counters of a migration.
func UpdateMigrationStatus(ctx context.Context, id int64, status string, state *string, completedDBs, failedDBs int) error {
	conn, err := getDB()
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET status = ? WHERE id = ?
	`, TableMigrations), status, id)

	return err
}

// StartMigration marks a migration as running and sets the start time.
func StartMigration(ctx context.Context, id int64, totalDBs int) error {
	conn, err := getDB()
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET status = ? WHERE id = ?
	`, TableMigrations), MigrationStatusRunning, id)

	return err
}

// CreateTemplateVersion creates a new version entry in templates_history.
func CreateTemplateVersion(ctx context.Context, templateID int32, version int, schema Schema) (string, error) {
	conn, err := getDB()
	if err != nil {
		return "", err
	}

	schemaJSON, err := tools.EncodeSchema(schema)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(schemaJSON)
	checksum := hex.EncodeToString(hash[:])

	now := time.Now().UTC().Format(time.RFC3339)

	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (template_id, version, schema, checksum, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, TableTemplatesHistory), templateID, version, schemaJSON, checksum, now)
	if err != nil {
		return "", err
	}

	return checksum, nil
}

// UpdateTemplateCurrentVersion updates the template's current_version.
func UpdateTemplateCurrentVersion(ctx context.Context, templateID int32, version int) error {
	conn, err := getDB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = conn.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s SET current_version = ?, updated_at = ?
		WHERE id = ?
	`, TableTemplates), version, now, templateID)

	return err
}

// GetMigrationSQL retrieves the SQL statements for a specific version transition.
func GetMigrationSQL(ctx context.Context, templateID int32, fromVersion, toVersion int) ([]string, error) {
	conn, err := getDB()
	if err != nil {
		return nil, err
	}

	row := conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT sql FROM %s
		WHERE template_id = ? AND from_version = ? AND to_version = ?
	`, TableMigrations), templateID, fromVersion, toVersion)

	var sqlJSON string
	if err := row.Scan(&sqlJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("migration not found: %d -> %d", fromVersion, toVersion)
		}
		return nil, err
	}

	var statements []string
	if err := json.Unmarshal([]byte(sqlJSON), &statements); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SQL: %w", err)
	}

	return statements, nil
}
