package data

import (
	"fmt"
	"strings"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/tools"
)

// sanitizeJSONKey validates and escapes a string for use as a JSON object key in SQL.
// Returns an error if the key contains invalid characters.
func sanitizeJSONKey(key string) (string, error) {
	if key == "" {
		return "", nil
	}
	if err := tools.ValidateIdentifier(key); err != nil {
		return "", fmt.Errorf("invalid alias: %w", err)
	}
	// Escape single quotes for SQL string literal safety
	return strings.ReplaceAll(key, "'", "''"), nil
}

// Relation represents a table with optional column selections and joins.
type Relation struct {
	name    string
	alias   string
	inner   bool
	columns []column
	joins   []*Relation
	parent  *Relation
}

type column struct {
	name  string
	alias string
}

// findForeignKey searches for a foreign key relationship between two tables.
// Returns an empty Fk if no relationship exists. Callers must check for empty Fk.
func (schema SchemaCache) findForeignKey(table, references string) CacheFk {
	// Error intentionally ignored - returns empty Fk when not found, which callers check
	fk, _ := schema.SearchFks(table, references)
	return fk
}

// relationDepth calculates the maximum nesting depth of a Relation tree.
func relationDepth(rel *Relation) int {
	if rel == nil || len(rel.joins) == 0 {
		return 1
	}
	maxChild := 0
	for _, join := range rel.joins {
		if d := relationDepth(join); d > maxChild {
			maxChild = d
		}
	}
	return 1 + maxChild
}

// buildJSONAggregation builds a json_object expression from key-value pairs.
// If pairs exceed MaxSelectColumns, chunks them and wraps with json_patch.
// Each pair is a string like "'colname', [colname]" (the key and value for json_object).
// Returns the complete expression: either "json_object(...)" or "json_patch(json_object(...), ...)".
func buildJSONAggregation(pairs []string) string {
	if len(pairs) == 0 {
		return "json_object()"
	}

	// If within limit, use simple json_object
	if len(pairs) <= MaxSelectColumns {
		return "json_object(" + strings.Join(pairs, ", ") + ")"
	}

	// Chunk into groups of MaxSelectColumns and use json_patch to merge
	var chunks []string
	for i := 0; i < len(pairs); i += MaxSelectColumns {
		end := i + MaxSelectColumns
		if end > len(pairs) {
			end = len(pairs)
		}
		chunk := "json_object(" + strings.Join(pairs[i:end], ", ") + ")"
		chunks = append(chunks, chunk)
	}

	// Nest json_patch calls: json_patch(json_patch(a, b), c)
	result := chunks[0]
	for i := 1; i < len(chunks); i++ {
		result = fmt.Sprintf("json_patch(%s, %s)", result, chunks[i])
	}

	return result
}

// buildSelect constructs a SELECT query with JSON aggregation for the root relation.
func (schema SchemaCache) buildSelect(rel Relation, policies selectPolicySet) (string, string, []any, error) {
	// Check query depth limit
	if depth := relationDepth(&rel); depth > config.Cfg.MaxQueryDepth {
		return "", "", nil, fmt.Errorf("%w: depth %d exceeds limit %d", tools.ErrQueryTooDeep, depth, config.Cfg.MaxQueryDepth)
	}

	var aggPairs []string
	sel := ""
	joins := ""
	var policyArgs []any

	if rel.columns == nil && rel.joins == nil {
		rel.columns = []column{{"*", ""}}
	}

	tbl, err := schema.SearchTbls(rel.name)
	if err != nil {
		return "", "", nil, err
	}

	for _, col := range rel.columns {
		if col.name == "*" {
			sel += "*, "
			for c, t := range tbl.Columns {
				if strings.EqualFold(t, ColTypeBlob) {
					continue
				}
				aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s]", c, c))
			}
			continue
		}

		column, err := tbl.SearchCols(col.name)
		if err != nil {
			return "", "", nil, err
		}

		if strings.EqualFold(column, ColTypeBlob) {
			continue
		}

		sel += fmt.Sprintf("[%s].[%s], ", rel.name, col.name)
		if col.alias != "" {
			sanitized, err := sanitizeJSONKey(col.alias)
			if err != nil {
				return "", "", nil, err
			}
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s]", sanitized, col.name))
		} else {
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s]", col.name, col.name))
		}
	}

	for _, joinTbl := range rel.joins {
		if joinTbl.alias != "" {
			sanitized, err := sanitizeJSONKey(joinTbl.alias)
			if err != nil {
				return "", "", nil, err
			}
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', json([%s])", sanitized, joinTbl.name))
		} else {
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', json([%s])", joinTbl.name, joinTbl.name))
		}
		query, aggs, joinArgs, err := schema.buildSelCurr(*joinTbl, rel.name, policies)
		if err != nil {
			return "", "", nil, err
		}
		policyArgs = append(policyArgs, joinArgs...)

		fk := schema.findForeignKey(joinTbl.name, rel.name)
		if fk == (CacheFk{}) {
			return "", "", nil, tools.NoRelationshipErr(rel.name, joinTbl.name)
		}

		sel += fmt.Sprintf("json_group_array(%s) FILTER (WHERE [%s].[%s] IS NOT NULL) AS [%s], ", aggs, fk.Table, fk.From, joinTbl.name)

		if joinTbl.inner {
			joins += "INNER "
		} else {
			joins += "LEFT "
		}

		joins += fmt.Sprintf("JOIN (%s) AS [%s] ON [%s].[%s] = [%s].[%s] ", query, joinTbl.name, fk.References, fk.To, fk.Table, fk.From)
	}

	query := "SELECT " + sel[:len(sel)-2] + fmt.Sprintf(" FROM [%s] ", rel.name) + joins
	if predicate, ok := policies[rel.name]; ok && predicate.SQL != "" {
		query += "WHERE " + predicate.SQL + " "
		policyArgs = append(policyArgs, predicate.Args...)
	}

	// When there are joins, we need GROUP BY on root table columns to properly aggregate nested relations
	if len(rel.joins) > 0 {
		var rootGroupBy string
		for _, col := range rel.columns {
			if col.name != "*" {
				rootGroupBy += fmt.Sprintf("[%s].[%s], ", rel.name, col.name)
			} else {
				// Group by all columns of the root table
				for _, c := range tbl.Columns {
					rootGroupBy += fmt.Sprintf("[%s].[%s], ", rel.name, c)
				}
			}
		}
		// Also group by rowid if table has no explicit PK
		if len(tbl.Pk) == 0 {
			rootGroupBy += fmt.Sprintf("[%s].[rowid], ", rel.name)
		} else {
			for _, pkCol := range tbl.Pk {
				rootGroupBy += fmt.Sprintf("[%s].[%s], ", rel.name, pkCol)
			}
		}
		if rootGroupBy != "" {
			query += "GROUP BY " + rootGroupBy[:len(rootGroupBy)-2] + " "
		}
	}

	return query, buildJSONAggregation(aggPairs), policyArgs, nil
}

// buildSelCurr constructs a SELECT query for a nested/joined relation.
func (schema SchemaCache) buildSelCurr(rel Relation, joinedOn string, policies selectPolicySet) (string, string, []any, error) {
	var sel string
	var joins string
	var aggPairs []string
	includesFk := false
	var fk CacheFk
	var policyArgs []any

	if rel.columns == nil && rel.joins == nil {
		rel.columns = []column{{"*", ""}}
	}

	tbl, err := schema.SearchTbls(rel.name)
	if err != nil {
		return "", "", nil, err
	}

	if joinedOn != "" {
		fk = schema.findForeignKey(rel.name, joinedOn)
	}

	for _, col := range rel.columns {
		if joinedOn != "" && fk.Table == rel.name && fk.From == col.name {
			includesFk = true
		}

		if col.name == "*" {
			sel += "*, "
			for c, t := range tbl.Columns {
				if strings.EqualFold(t, ColTypeBlob) {
					continue
				}
				aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s].[%s]", c, rel.name, c))
			}
			continue
		}

		colType, err := tbl.SearchCols(col.name)
		if err != nil {
			return "", "", nil, err
		}

		if strings.EqualFold(colType, ColTypeBlob) {
			continue
		}

		sel += fmt.Sprintf("[%s].[%s], ", rel.name, col.name)
		if col.alias != "" {
			sanitized, err := sanitizeJSONKey(col.alias)
			if err != nil {
				return "", "", nil, err
			}
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s].[%s]", sanitized, rel.name, col.name))
		} else {
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s].[%s]", col.name, rel.name, col.name))
		}
	}

	if !includesFk && fk.Table != "" {
		sel += fmt.Sprintf("[%s].[%s], ", fk.Table, fk.From)
	}

	for _, joinTbl := range rel.joins {
		if joinTbl.alias != "" {
			sanitized, err := sanitizeJSONKey(joinTbl.alias)
			if err != nil {
				return "", "", nil, err
			}
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', json([%s])", sanitized, joinTbl.name))
		} else {
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', json([%s])", joinTbl.name, joinTbl.name))
		}
		query, aggs, joinArgs, err := schema.buildSelCurr(*joinTbl, rel.name, policies)
		if err != nil {
			return "", "", nil, err
		}
		policyArgs = append(policyArgs, joinArgs...)

		nestedFk := schema.findForeignKey(joinTbl.name, rel.name)
		if nestedFk == (CacheFk{}) {
			return "", "", nil, tools.NoRelationshipErr(rel.name, joinTbl.name)
		}

		sel += fmt.Sprintf("json_group_array(%s) FILTER (WHERE [%s].[%s] IS NOT NULL) AS [%s], ", aggs, nestedFk.Table, nestedFk.From, joinTbl.name)

		if joinTbl.inner {
			joins += "INNER "
		} else {
			joins += "LEFT "
		}

		joins += fmt.Sprintf("JOIN (%s) AS [%s] ON [%s].[%s] = [%s].[%s] ", query, joinTbl.name, nestedFk.References, nestedFk.To, nestedFk.Table, nestedFk.From)
	}

	query := "SELECT " + sel[:len(sel)-2] + fmt.Sprintf(" FROM [%s] ", rel.name) + joins
	if predicate, ok := policies[rel.name]; ok && predicate.SQL != "" {
		query += "WHERE " + predicate.SQL + " "
		policyArgs = append(policyArgs, predicate.Args...)
	}

	return query, buildJSONAggregation(aggPairs), policyArgs, nil
}

// CustomJoinQuery represents a SELECT query with custom joins.
type CustomJoinQuery struct {
	BaseTable     string              // Primary table to query from
	BaseColumns   []column            // Columns from base table
	Joins         []customJoin        // Custom join definitions
	JoinedColumns map[string][]column // Columns per joined table: table -> columns
}

type customJoin struct {
	table      string          // Table to join
	alias      string          // Optional alias (defaults to table name)
	joinType   string          // "left" or "inner"
	conditions []joinCondition // ON conditions
	flat       bool            // If true, flatten output
}

type joinCondition struct {
	leftTable  string // Left side table
	leftCol    string // Left side column
	op         string // Operator (eq, gt, etc.)
	rightTable string // Right side table
	rightCol   string // Right side column
}

// ParseCustomJoinQuery parses a SelectQuery with custom joins into a structured query.
func (schema SchemaCache) ParseCustomJoinQuery(baseTable string, query SelectQuery) (*CustomJoinQuery, error) {
	result := &CustomJoinQuery{
		BaseTable:     baseTable,
		JoinedColumns: make(map[string][]column),
	}

	// Build map of joined tables for lookup
	joinedTables := make(map[string]bool)
	for _, j := range query.Join {
		alias := j.Alias
		if alias == "" {
			alias = j.Table
		}
		joinedTables[alias] = true
		joinedTables[j.Table] = true
	}

	// Parse select columns - separate base table columns from joined table columns
	for _, item := range query.Select {
		switch v := item.(type) {
		case string:
			if v == "*" {
				result.BaseColumns = append(result.BaseColumns, column{name: "*", alias: ""})
				continue
			}
			// Check if it's a table.column format
			if parts := splitTableColumn(v); len(parts) == 2 {
				tableName, colName := parts[0], parts[1]
				if joinedTables[tableName] {
					result.JoinedColumns[tableName] = append(result.JoinedColumns[tableName], column{name: colName, alias: ""})
				} else if tableName == baseTable {
					result.BaseColumns = append(result.BaseColumns, column{name: colName, alias: ""})
				} else {
					return nil, fmt.Errorf("unknown table in select: %s", tableName)
				}
			} else {
				// No table prefix - belongs to base table
				result.BaseColumns = append(result.BaseColumns, column{name: v, alias: ""})
			}
		case map[string]any:
			// Handle aliased columns: {"alias": "column"} or {"alias": "table.column"}
			for alias, val := range v {
				if colName, ok := val.(string); ok {
					if parts := splitTableColumn(colName); len(parts) == 2 {
						tableName, col := parts[0], parts[1]
						if joinedTables[tableName] {
							result.JoinedColumns[tableName] = append(result.JoinedColumns[tableName], column{name: col, alias: alias})
						} else if tableName == baseTable {
							result.BaseColumns = append(result.BaseColumns, column{name: col, alias: alias})
						} else {
							return nil, fmt.Errorf("unknown table in select: %s", tableName)
						}
					} else {
						result.BaseColumns = append(result.BaseColumns, column{name: colName, alias: alias})
					}
				}
			}
		}
	}

	// Default to * if no base columns specified
	if len(result.BaseColumns) == 0 {
		result.BaseColumns = []column{{name: "*", alias: ""}}
	}

	// Parse join clauses
	for _, j := range query.Join {
		cj := customJoin{
			table:    j.Table,
			alias:    j.Alias,
			joinType: j.Type,
			flat:     j.Flat,
		}
		if cj.alias == "" {
			cj.alias = j.Table
		}
		if cj.joinType == "" {
			cj.joinType = JoinTypeLeft
		}

		// Validate join type
		if cj.joinType != JoinTypeLeft && cj.joinType != JoinTypeInner {
			return nil, fmt.Errorf("invalid join type: %s (must be 'left' or 'inner')", cj.joinType)
		}

		// Validate joined table exists
		if _, err := schema.SearchTbls(j.Table); err != nil {
			return nil, err
		}

		// Parse ON conditions
		for _, cond := range j.On {
			jc, err := parseJoinCondition(cond)
			if err != nil {
				return nil, err
			}
			cj.conditions = append(cj.conditions, jc)
		}

		if len(cj.conditions) == 0 {
			return nil, fmt.Errorf("join on table %s requires at least one ON condition", j.Table)
		}

		result.Joins = append(result.Joins, cj)

		// Default columns for joined table if none specified
		if _, exists := result.JoinedColumns[cj.alias]; !exists {
			result.JoinedColumns[cj.alias] = []column{{name: "*", alias: ""}}
		}
	}

	return result, nil
}

// parseJoinCondition parses a single join condition from JSON format.
// Format: {"users.id": {"eq": "orders.user_id"}}
func parseJoinCondition(cond map[string]any) (joinCondition, error) {
	if len(cond) != 1 {
		return joinCondition{}, fmt.Errorf("join condition must have exactly one key")
	}

	var jc joinCondition
	for leftSide, opValue := range cond {
		// Parse left side: "table.column"
		leftParts := splitTableColumn(leftSide)
		if len(leftParts) != 2 {
			return joinCondition{}, fmt.Errorf("join condition left side must be table.column format: %s", leftSide)
		}
		jc.leftTable = leftParts[0]
		jc.leftCol = leftParts[1]

		// Parse operator and right side
		opMap, ok := opValue.(map[string]any)
		if !ok {
			return joinCondition{}, fmt.Errorf("join condition value must be an object with operator")
		}

		for op, rightSide := range opMap {
			jc.op = op
			rightStr, ok := rightSide.(string)
			if !ok {
				return joinCondition{}, fmt.Errorf("join condition right side must be a column reference string")
			}
			rightParts := splitTableColumn(rightStr)
			if len(rightParts) != 2 {
				return joinCondition{}, fmt.Errorf("join condition right side must be table.column format: %s", rightStr)
			}
			jc.rightTable = rightParts[0]
			jc.rightCol = rightParts[1]
			break // Only one operator per condition
		}
		break // Only one key-value pair
	}

	return jc, nil
}

// splitTableColumn splits "table.column" into ["table", "column"].
// Returns single element slice if no dot is present.
func splitTableColumn(s string) []string {
	idx := strings.Index(s, ".")
	if idx == -1 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+1:]}
}

// BuildCustomJoinSelect builds a SELECT query with custom joins.
// Returns: (selectQuery, groupByClause, jsonAggregation, error)
// The caller must place WHERE between selectQuery and groupByClause.
func (schema SchemaCache) BuildCustomJoinSelect(cjq *CustomJoinQuery, policies selectPolicySet) (string, string, string, []any, error) {
	var aggPairs []string
	sel := ""
	joins := ""
	var args []any

	// Get base table schema
	baseTbl, err := schema.SearchTbls(cjq.BaseTable)
	if err != nil {
		return "", "", "", nil, err
	}

	// Build base table columns
	// Note: aggPairs uses just [colname] without table prefix because it's used in the
	// outer json_object() which selects FROM the inner subquery, not directly from tables.
	for _, col := range cjq.BaseColumns {
		if col.name == "*" {
			sel += fmt.Sprintf("[%s].*, ", cjq.BaseTable)
			for c, t := range baseTbl.Columns {
				if strings.EqualFold(t, ColTypeBlob) {
					continue
				}
				aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s]", c, c))
			}
		} else {
			colType, err := baseTbl.SearchCols(col.name)
			if err != nil {
				return "", "", "", nil, err
			}
			if strings.EqualFold(colType, ColTypeBlob) {
				continue
			}
			sel += fmt.Sprintf("[%s].[%s], ", cjq.BaseTable, col.name)
			key := col.name
			if col.alias != "" {
				key = col.alias
			}
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s]", key, col.name))
		}
	}

	// Check if any join requires nesting (flat=false)
	hasNestedJoin := false
	for _, j := range cjq.Joins {
		if !j.flat {
			hasNestedJoin = true
			break
		}
	}

	// Build joins and joined table columns
	for _, j := range cjq.Joins {
		joinTbl, err := schema.SearchTbls(j.table)
		if err != nil {
			return "", "", "", nil, err
		}

		// Build ON clause
		onClause := ""
		for i, cond := range j.conditions {
			if i > 0 {
				onClause += " AND "
			}
			sqlOp := opToSQL(cond.op)
			onClause += fmt.Sprintf("[%s].[%s] %s [%s].[%s]",
				cond.leftTable, cond.leftCol, sqlOp, cond.rightTable, cond.rightCol)
		}

		// Add JOIN clause
		if j.joinType == JoinTypeInner {
			joins += "INNER "
		} else {
			joins += "LEFT "
		}
		joins += fmt.Sprintf("JOIN [%s] ", j.table)
		if j.alias != j.table {
			joins += fmt.Sprintf("AS [%s] ", j.alias)
		}
		if predicate, ok := policies[j.table]; ok && predicate.SQL != "" {
			onClause = "(" + onClause + ") AND (" + predicate.SQL + ")"
			args = append(args, predicate.Args...)
		}
		joins += fmt.Sprintf("ON %s ", onClause)

		// Build columns for this joined table
		joinedCols := cjq.JoinedColumns[j.alias]
		if j.flat {
			// Flat output: add columns directly to select with AS alias
			// Using explicit aliases so the outer json_object can reference them
			for _, col := range joinedCols {
				if col.name == "*" {
					for c, t := range joinTbl.Columns {
						if strings.EqualFold(t, ColTypeBlob) {
							continue
						}
						// Prefix with table name for flat output to avoid conflicts
						key := fmt.Sprintf("%s_%s", j.alias, c)
						sel += fmt.Sprintf("[%s].[%s] AS [%s], ", j.alias, c, key)
						aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s]", key, key))
					}
				} else {
					colType, err := joinTbl.SearchCols(col.name)
					if err != nil {
						return "", "", "", nil, err
					}
					if strings.EqualFold(colType, ColTypeBlob) {
						continue
					}
					key := col.name
					if col.alias != "" {
						key = col.alias
					} else {
						key = fmt.Sprintf("%s_%s", j.alias, col.name)
					}
					sel += fmt.Sprintf("[%s].[%s] AS [%s], ", j.alias, col.name, key)
					aggPairs = append(aggPairs, fmt.Sprintf("'%s', [%s]", key, key))
				}
			}
		} else {
			// Nested output: aggregate into JSON array
			var nestedPairs []string
			for _, col := range joinedCols {
				if col.name == "*" {
					for c, t := range joinTbl.Columns {
						if strings.EqualFold(t, ColTypeBlob) {
							continue
						}
						nestedPairs = append(nestedPairs, fmt.Sprintf("'%s', [%s].[%s]", c, j.alias, c))
					}
				} else {
					colType, err := joinTbl.SearchCols(col.name)
					if err != nil {
						return "", "", "", nil, err
					}
					if strings.EqualFold(colType, ColTypeBlob) {
						continue
					}
					key := col.name
					if col.alias != "" {
						key = col.alias
					}
					nestedPairs = append(nestedPairs, fmt.Sprintf("'%s', [%s].[%s]", key, j.alias, col.name))
				}
			}

			// Get the first join condition to determine the filter column for FILTER clause
			filterCol := fmt.Sprintf("[%s].[%s]", j.conditions[0].rightTable, j.conditions[0].rightCol)
			if j.conditions[0].rightTable != j.table && j.conditions[0].rightTable != j.alias {
				filterCol = fmt.Sprintf("[%s].[%s]", j.conditions[0].leftTable, j.conditions[0].leftCol)
			}

			// Build nested JSON aggregation
			nestedAgg := buildJSONAggregation(nestedPairs)
			sel += fmt.Sprintf("json_group_array(%s) FILTER (WHERE %s IS NOT NULL) AS [%s], ", nestedAgg, filterCol, j.alias)
			aggPairs = append(aggPairs, fmt.Sprintf("'%s', json([%s])", j.alias, j.alias))
		}
	}

	if sel == "" {
		return "", "", "", nil, fmt.Errorf("no columns selected")
	}
	sel = sel[:len(sel)-2] // Remove trailing ", "

	query := fmt.Sprintf("SELECT %s FROM [%s] %s", sel, cjq.BaseTable, joins)
	if predicate, ok := policies[cjq.BaseTable]; ok && predicate.SQL != "" {
		query += "WHERE " + predicate.SQL + " "
		args = append(args, predicate.Args...)
	}

	// Build GROUP BY for nested output (returned separately so caller can place WHERE before it)
	var groupByClause string
	if hasNestedJoin {
		var groupBy []string
		for _, col := range cjq.BaseColumns {
			if col.name == "*" {
				for c, _ := range baseTbl.Columns {
					groupBy = append(groupBy, fmt.Sprintf("[%s].[%s]", cjq.BaseTable, c))
				}
			} else {
				groupBy = append(groupBy, fmt.Sprintf("[%s].[%s]", cjq.BaseTable, col.name))
			}
		}
		// Add primary key columns to group by if not already included
		if len(baseTbl.Pk) > 0 {
			for _, pkCol := range baseTbl.Pk {
				pkIncluded := false
				for _, col := range cjq.BaseColumns {
					if col.name == pkCol || col.name == "*" {
						pkIncluded = true
						break
					}
				}
				if !pkIncluded {
					groupBy = append(groupBy, fmt.Sprintf("[%s].[%s]", cjq.BaseTable, pkCol))
				}
			}
		} else {
			groupBy = append(groupBy, fmt.Sprintf("[%s].[rowid]", cjq.BaseTable))
		}
		groupByClause = "GROUP BY " + strings.Join(groupBy, ", ") + " "
	}

	return query, groupByClause, buildJSONAggregation(aggPairs), args, nil
}

// opToSQL converts a filter operator to SQL operator.
func opToSQL(op string) string {
	switch op {
	case OpEq:
		return "="
	case OpNeq:
		return "!="
	case OpGt:
		return ">"
	case OpGte:
		return ">="
	case OpLt:
		return "<"
	case OpLte:
		return "<="
	default:
		return "="
	}
}

// parseSelect parses a select parameter string into a Relation tree.
// Syntax: "col1,col2,related_table(col1,col2),other_table(!)"
//   - Parentheses denote related tables (joins) when preceded by a table name
//   - ! marks an inner join
//   - : provides an alias (e.g., "alias:column")
//   - Quotes allow special characters in names
//   - Backslash escapes the next character
func parseSelect(param string, table string) Relation {
	tbl := Relation{table, "", false, nil, nil, nil}
	currTbl := &tbl
	currStr := ""
	alias := ""
	inner := false
	quoted := false
	escaped := false

	for _, v := range param {
		if escaped {
			currStr += string(v)
			escaped = false
			continue
		}
		if v == '\\' {
			escaped = true
			continue
		}
		if quoted && v != '"' {
			currStr += string(v)
			continue
		}
		switch v {
		case '"':
			quoted = !quoted
		case '(':
			// It's a relation/join
			currTbl = &Relation{currStr, alias, inner, nil, nil, currTbl}
			currTbl.parent.joins = append(currTbl.parent.joins, currTbl)
			currStr = ""
			alias = ""
			inner = false
		case ')':
			if currStr != "" {
				currTbl.columns = append(currTbl.columns, column{currStr, alias})
			}
			currTbl = currTbl.parent
			currStr = ""
			alias = ""
		case ':':
			alias = currStr
			currStr = ""
		case '!':
			inner = true
		case ',':
			if currStr == "" {
				continue
			}
			currTbl.columns = append(currTbl.columns, column{currStr, alias})
			alias = ""
			currStr = ""
		default:
			currStr += string(v)
		}
	}

	if currStr == "" {
		return tbl
	}

	currTbl.columns = append(currTbl.columns, column{currStr, alias})

	return tbl
}
