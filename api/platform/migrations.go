package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// GenerateMigrationPlan creates a migration plan from schema diff and merges.
// Merges convert drop+add pairs into renames by index in the changes array.
func GenerateMigrationPlan(oldSchema, newSchema Schema, changes []SchemaDiff, merges []Merge) (*MigrationPlan, error) {
	// Apply merges to convert drop+add pairs to renames
	renames := applyMerges(changes, merges)

	// Build sets of renamed items to skip in add/drop processing
	renamedTables := make(map[string]string)
	renamedColumns := make(map[string]string)
	for _, r := range renames {
		if r.Type == "rename_table" {
			renamedTables[r.OldName] = r.NewName
		} else if r.Type == "rename_column" {
			key := r.Table + "." + r.OldName
			renamedColumns[key] = r.NewName
		}
	}

	var statements []string

	// 1. Renames first
	for _, r := range renames {
		if r.Type == "rename_table" {
			statements = append(statements, fmt.Sprintf(
				"ALTER TABLE [%s] RENAME TO [%s]", r.OldName, r.NewName))
		} else if r.Type == "rename_column" {
			statements = append(statements, fmt.Sprintf(
				"ALTER TABLE [%s] RENAME COLUMN [%s] TO [%s]", r.Table, r.OldName, r.NewName))
		}
	}

	var addTables, dropTables []SchemaDiff
	var addColumns, dropColumns, modifyColumns []SchemaDiff
	var addIndexes, dropIndexes []SchemaDiff
	var addFTS, dropFTS []SchemaDiff
	var pkTypeChanges []SchemaDiff

	mergedIndices := getMergedIndices(merges)

	for i, c := range changes {
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

	oldTables := make(map[string]Table)
	for _, t := range oldSchema.Tables {
		oldTables[t.Name] = t
	}
	newTables := make(map[string]Table)
	for _, t := range newSchema.Tables {
		newTables[t.Name] = t
	}

	for _, c := range addTables {
		if table, ok := newTables[c.Table]; ok {
			sql := generateCreateTableSQL(table)
			statements = append(statements, sql)
			for _, idx := range table.Indexes {
				statements = append(statements, generateCreateIndexSQL(c.Table, idx))
			}
			if len(table.FTSColumns) > 0 {
				statements = append(statements, generateFTSSQL(c.Table, table.FTSColumns, table.Pk)...)
			}
		}
	}

	for _, c := range addColumns {
		table := newTables[c.Table]
		col := table.Columns[c.Column]
		if requiresMirrorTable(Col{}, col) {
			mirrorSQL := generateMirrorTableSQL(oldTables[c.Table], table)
			statements = append(statements, mirrorSQL...)
		} else {
			sql := generateAddColumnSQL(c.Table, col)
			statements = append(statements, sql)
		}
	}

	for _, c := range modifyColumns {
		oldTable := oldTables[c.Table]
		newTable := newTables[c.Table]
		oldCol := oldTable.Columns[c.Column]
		newCol := newTable.Columns[c.Column]

		if requiresMirrorTable(oldCol, newCol) {
			mirrorSQL := generateMirrorTableSQL(oldTable, newTable)
			statements = append(statements, mirrorSQL...)
		}
	}

	for _, c := range pkTypeChanges {
		oldTable := oldTables[c.Table]
		newTable := newTables[c.Table]
		mirrorSQL := generateMirrorTableSQL(oldTable, newTable)
		statements = append(statements, mirrorSQL...)
	}

	for _, c := range addIndexes {
		table := newTables[c.Table]
		for _, idx := range table.Indexes {
			if idx.Name == c.Column {
				sql := generateCreateIndexSQL(c.Table, idx)
				statements = append(statements, sql)
				break
			}
		}
	}

	for _, c := range addFTS {
		table := newTables[c.Table]
		if len(table.FTSColumns) > 0 {
			ftsSQL := generateFTSSQL(c.Table, table.FTSColumns, table.Pk)
			statements = append(statements, ftsSQL...)
		}
	}

	for _, c := range dropFTS {
		ftsSQL := generateDropFTSSQL(c.Table)
		statements = append(statements, ftsSQL...)
	}

	for _, c := range dropIndexes {
		statements = append(statements, fmt.Sprintf("DROP INDEX IF EXISTS [%s]", c.Column))
	}

	for _, c := range dropColumns {
		statements = append(statements, fmt.Sprintf(
			"ALTER TABLE [%s] DROP COLUMN [%s]", c.Table, c.Column))
	}

	for _, c := range dropTables {
		statements = append(statements, fmt.Sprintf("DROP TABLE IF EXISTS [%s]", c.Table))
	}

	return &MigrationPlan{SQL: statements}, nil
}

type rename struct {
	Type    string
	Table   string
	OldName string
	NewName string
}

func applyMerges(changes []SchemaDiff, merges []Merge) []rename {
	var renames []rename

	for _, m := range merges {
		if m.Old < 0 || m.Old >= len(changes) || m.New < 0 || m.New >= len(changes) {
			continue
		}

		dropChange := changes[m.Old]
		addChange := changes[m.New]

		if dropChange.Type == "drop_table" && addChange.Type == "add_table" {
			renames = append(renames, rename{
				Type:    "rename_table",
				OldName: dropChange.Table,
				NewName: addChange.Table,
			})
		}

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

func getMergedIndices(merges []Merge) map[int]bool {
	indices := make(map[int]bool)
	for _, m := range merges {
		indices[m.Old] = true
		indices[m.New] = true
	}
	return indices
}

func requiresMirrorTable(old, new Col) bool {
	if old.References == "" && new.References != "" {
		return true
	}
	if old.References != "" && new.References != "" {
		if old.References != new.References || old.OnDelete != new.OnDelete || old.OnUpdate != new.OnUpdate {
			return true
		}
	}
	if old.References != "" && new.References == "" {
		return true
	}
	if old.Check != new.Check {
		return true
	}
	if old.Collate != new.Collate {
		return true
	}
	if old.Generated == nil && new.Generated != nil {
		return true
	}
	if old.Generated != nil && new.Generated == nil {
		return true
	}
	if old.Generated != nil && new.Generated != nil {
		if old.Generated.Expr != new.Generated.Expr || old.Generated.Stored != new.Generated.Stored {
			return true
		}
	}
	return false
}

// generateCreateTableSQL generates CREATE TABLE statement.
// Non-PK columns are typeless; types are looked up from schema metadata at query time.
func generateCreateTableSQL(t Table) string {
	var cols []string
	var fks []string

	colNames := make([]string, 0, len(t.Columns))
	for name := range t.Columns {
		colNames = append(colNames, name)
	}
	sort.Strings(colNames)

	for _, name := range colNames {
		col := t.Columns[name]
		colDef := generateColumnDef(col, t.Pk)
		cols = append(cols, colDef)

		if col.References != "" {
			fk := generateFKConstraint(col)
			fks = append(fks, fk)
		}
	}

	if len(t.Pk) > 1 {
		pkCols := make([]string, len(t.Pk))
		for i, pk := range t.Pk {
			pkCols[i] = "[" + pk + "]"
		}
		cols = append(cols, "PRIMARY KEY ("+strings.Join(pkCols, ", ")+")")
	}

	cols = append(cols, fks...)
	return fmt.Sprintf("CREATE TABLE [%s] (\n  %s\n)", t.Name, strings.Join(cols, ",\n  "))
}

func generateColumnDef(col Col, pk []string) string {
	var parts []string
	parts = append(parts, "["+col.Name+"]")

	isPK := len(pk) == 1 && pk[0] == col.Name
	isCompositePK := false
	for _, p := range pk {
		if p == col.Name {
			isCompositePK = true
			break
		}
	}

	if isPK {
		colType := strings.ToUpper(col.Type)
		if colType == "INTEGER" || colType == "INT" {
			parts = append(parts, "INTEGER PRIMARY KEY")
		} else {
			parts = append(parts, colType, "PRIMARY KEY")
		}
	} else if isCompositePK && strings.ToUpper(col.Type) == "INTEGER" {
		parts = append(parts, "INTEGER")
	}

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

func generateFKConstraint(col Col) string {
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

func generateAddColumnSQL(table string, col Col) string {
	var parts []string
	parts = append(parts, "["+col.Name+"]")

	if col.NotNull {
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

	return fmt.Sprintf("ALTER TABLE [%s] ADD COLUMN %s", table, strings.Join(parts, " "))
}

func generateMirrorTableSQL(oldTable, newTable Table) []string {
	tempName := newTable.Name + "_new"

	createSQL := generateCreateTableSQL(Table{
		Name:       tempName,
		Pk:         newTable.Pk,
		Columns:    newTable.Columns,
		Indexes:    nil,
		FTSColumns: nil,
	})

	var oldCols, newCols []string
	for colName, newCol := range newTable.Columns {
		if oldCol, exists := oldTable.Columns[colName]; exists {
			oldCols = append(oldCols, "["+colName+"]")
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

	sort.Strings(oldCols)
	sort.Strings(newCols)

	copySQL := fmt.Sprintf("INSERT INTO [%s] (%s) SELECT %s FROM [%s]",
		tempName, strings.Join(oldCols, ", "), strings.Join(newCols, ", "), oldTable.Name)

	dropSQL := fmt.Sprintf("DROP TABLE [%s]", oldTable.Name)
	renameSQL := fmt.Sprintf("ALTER TABLE [%s] RENAME TO [%s]", tempName, newTable.Name)

	statements := []string{createSQL, copySQL, dropSQL, renameSQL}
	for _, idx := range newTable.Indexes {
		statements = append(statements, generateCreateIndexSQL(newTable.Name, idx))
	}
	if len(newTable.FTSColumns) > 0 {
		statements = append(statements, generateFTSSQL(newTable.Name, newTable.FTSColumns, newTable.Pk)...)
	}
	return statements
}

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

func generateFTSSQL(table string, ftsColumns []string, pk []string) []string {
	ftsTable := table + "_fts"
	cols := make([]string, len(ftsColumns))
	for i, c := range ftsColumns {
		cols[i] = "[" + c + "]"
	}
	contentCols := strings.Join(cols, ", ")
	createFTS := fmt.Sprintf(
		"CREATE VIRTUAL TABLE IF NOT EXISTS [%s] USING fts5(%s, content=[%s], content_rowid=[%s])",
		ftsTable, contentCols, table, pk[0])

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

func generateDropFTSSQL(table string) []string {
	ftsTable := table + "_fts"
	return []string{
		fmt.Sprintf("DROP TRIGGER IF EXISTS [%s_ai]", ftsTable),
		fmt.Sprintf("DROP TRIGGER IF EXISTS [%s_ad]", ftsTable),
		fmt.Sprintf("DROP TRIGGER IF EXISTS [%s_au]", ftsTable),
		fmt.Sprintf("DROP TABLE IF EXISTS [%s]", ftsTable),
	}
}

func prefixColumns(cols []string, prefix string) string {
	result := make([]string, len(cols))
	for i, c := range cols {
		result[i] = prefix + "[" + c + "]"
	}
	return strings.Join(result, ", ")
}

func formatDefault(val any) string {
	if m, ok := val.(map[string]any); ok {
		if raw, ok := m["sql"].(string); ok && strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	if m, ok := val.(map[string]string); ok {
		if raw, ok := m["sql"]; ok && strings.TrimSpace(raw) != "" {
			return raw
		}
	}

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

type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (api *API) createMigration(ctx context.Context, definitionID int32, fromVersion, toVersion int, sqlStatements []string) (*Migration, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}
	return createMigrationTx(ctx, conn, definitionID, fromVersion, toVersion, sqlStatements)
}

func createMigrationTx(ctx context.Context, exec Execer, definitionID int32, fromVersion, toVersion int, sqlStatements []string) (*Migration, error) {
	now := time.Now().UTC()

	sqlJSON, err := json.Marshal(sqlStatements)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal SQL: %w", err)
	}

	result, err := exec.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (definition_id, from_version, to_version, sql, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, TableMigrations), definitionID, fromVersion, toVersion, string(sqlJSON), now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &Migration{
		ID:           id,
		DefinitionID: definitionID,
		FromVersion:  fromVersion,
		ToVersion:    toVersion,
		SQL:          sqlStatements,
		Status:       MigrationStatusPending,
		CreatedAt:    now,
	}, nil
}

func (api *API) getMigrationSQL(ctx context.Context, definitionID int32, fromVersion, toVersion int) ([]string, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	row := conn.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT sql FROM %s
		WHERE definition_id = ? AND from_version = ? AND to_version = ?
	`, TableMigrations), definitionID, fromVersion, toVersion)

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

func diffSchemas(old, new Schema) []SchemaDiff {
	var changes []SchemaDiff

	oldTables := make(map[string]Table)
	for _, t := range old.Tables {
		oldTables[t.Name] = t
	}
	newTables := make(map[string]Table)
	for _, t := range new.Tables {
		newTables[t.Name] = t
	}

	for name := range oldTables {
		if _, exists := newTables[name]; !exists {
			changes = append(changes, SchemaDiff{Type: "drop_table", Table: name})
		}
	}

	for name, newTable := range newTables {
		oldTable, exists := oldTables[name]
		if !exists {
			changes = append(changes, SchemaDiff{Type: "add_table", Table: name})
			continue
		}

		changes = append(changes, diffColumns(name, oldTable, newTable)...)
		changes = append(changes, diffIndexes(name, oldTable, newTable)...)
		changes = append(changes, diffFTS(name, oldTable, newTable)...)

		if pkTypeChanged(oldTable, newTable) {
			changes = append(changes, SchemaDiff{Type: "change_pk_type", Table: name})
		}
	}

	return changes
}

func diffColumns(tableName string, old, new Table) []SchemaDiff {
	var changes []SchemaDiff
	for colName := range old.Columns {
		if _, exists := new.Columns[colName]; !exists {
			changes = append(changes, SchemaDiff{Type: "drop_column", Table: tableName, Column: colName})
		}
	}
	for colName, newCol := range new.Columns {
		oldCol, exists := old.Columns[colName]
		if !exists {
			changes = append(changes, SchemaDiff{Type: "add_column", Table: tableName, Column: colName})
			continue
		}
		if columnModified(oldCol, newCol) {
			changes = append(changes, SchemaDiff{Type: "modify_column", Table: tableName, Column: colName})
		}
	}
	return changes
}

func diffIndexes(tableName string, old, new Table) []SchemaDiff {
	var changes []SchemaDiff
	oldIndexes := make(map[string]Index)
	for _, idx := range old.Indexes {
		oldIndexes[idx.Name] = idx
	}
	newIndexes := make(map[string]Index)
	for _, idx := range new.Indexes {
		newIndexes[idx.Name] = idx
	}
	for name := range oldIndexes {
		if _, exists := newIndexes[name]; !exists {
			changes = append(changes, SchemaDiff{Type: "drop_index", Table: tableName, Column: name})
		}
	}
	for name := range newIndexes {
		if _, exists := oldIndexes[name]; !exists {
			changes = append(changes, SchemaDiff{Type: "add_index", Table: tableName, Column: name})
		}
	}
	return changes
}

func diffFTS(tableName string, old, new Table) []SchemaDiff {
	var changes []SchemaDiff
	oldFTS := make(map[string]bool)
	for _, col := range old.FTSColumns {
		oldFTS[col] = true
	}
	newFTS := make(map[string]bool)
	for _, col := range new.FTSColumns {
		newFTS[col] = true
	}
	if len(oldFTS) == 0 && len(newFTS) > 0 {
		changes = append(changes, SchemaDiff{Type: "add_fts", Table: tableName})
	} else if len(oldFTS) > 0 && len(newFTS) == 0 {
		changes = append(changes, SchemaDiff{Type: "drop_fts", Table: tableName})
	} else if !equalStringMaps(oldFTS, newFTS) {
		changes = append(changes, SchemaDiff{Type: "drop_fts", Table: tableName})
		changes = append(changes, SchemaDiff{Type: "add_fts", Table: tableName})
	}
	return changes
}

func pkTypeChanged(old, new Table) bool {
	if len(old.Pk) != len(new.Pk) {
		return true
	}
	for i, pk := range old.Pk {
		if new.Pk[i] != pk {
			return true
		}
		oldCol, oldExists := old.Columns[pk]
		newCol, newExists := new.Columns[pk]
		if oldExists && newExists && oldCol.Type != newCol.Type {
			return true
		}
	}
	return false
}

func columnModified(old, new Col) bool {
	if old.Type != new.Type ||
		old.NotNull != new.NotNull ||
		old.Unique != new.Unique ||
		old.Collate != new.Collate ||
		old.Check != new.Check ||
		old.References != new.References ||
		old.OnDelete != new.OnDelete ||
		old.OnUpdate != new.OnUpdate {
		return true
	}
	if !equalDefaults(old.Default, new.Default) {
		return true
	}
	if !equalGenerated(old.Generated, new.Generated) {
		return true
	}
	return false
}

func equalDefaults(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

func equalGenerated(a, b *Generated) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Expr == b.Expr && a.Stored == b.Stored
}

func equalStringMaps(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "UNIQUE constraint failed") || strings.Contains(errStr, "unique constraint")
}
