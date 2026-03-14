package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/tools"
)

// SelectJSON queries rows using JSON body format.
// POST /data/query/{table} with Prefer: operation=select
func (dao *TenantConnection) SelectJSON(ctx context.Context, relation string, query SelectQuery, includeCount bool) (SelectResult, error) {
	return dao.selectJSON(ctx, dao.Client, relation, query, includeCount)
}

func (dao *TenantConnection) selectJSON(ctx context.Context, exec Executor, relation string, query SelectQuery, includeCount bool) (SelectResult, error) {
	if err := tools.ValidateTableName(relation); err != nil {
		return SelectResult{}, err
	}

	table, err := dao.Schema.SearchTbls(relation)
	if err != nil {
		return SelectResult{}, err
	}

	var sqlQuery, groupBy, agg string

	// Check if this is a custom join query
	if len(query.Join) > 0 {
		// Parse and build custom join query
		cjq, err := dao.Schema.ParseCustomJoinQuery(relation, query)
		if err != nil {
			return SelectResult{}, err
		}

		sqlQuery, groupBy, agg, err = dao.Schema.BuildCustomJoinSelect(cjq)
		if err != nil {
			return SelectResult{}, err
		}
	} else {
		// Parse select clause for implicit FK-based joins
		rel, err := ParseSelectFromJSON(query.Select, relation)
		if err != nil {
			return SelectResult{}, err
		}

		// Build SELECT query
		sqlQuery, agg, err = dao.Schema.buildSelect(rel)
		if err != nil {
			return SelectResult{}, err
		}
	}

	// Build WHERE clause
	where, args, err := table.BuildWhereFromJSON(query.Where, dao.Schema)
	if err != nil {
		return SelectResult{}, err
	}

	// Build query in correct SQL order: SELECT...FROM...JOIN + WHERE + GROUP BY
	baseQuery := sqlQuery + where + groupBy

	var result SelectResult

	// Get count if requested
	if includeCount {
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s)", baseQuery)
		row := exec.QueryRowContext(ctx, countQuery, args...)
		if err := row.Scan(&result.Count); err != nil {
			return SelectResult{}, err
		}
	}

	// Add ordering
	if query.Order != nil {
		order, err := table.BuildOrderFromJSON(query.Order)
		if err != nil {
			return SelectResult{}, err
		}
		baseQuery += order
	}

	// Handle pagination
	limit := config.Cfg.DefaultLimit
	if query.Limit != nil && *query.Limit >= 0 {
		limit = *query.Limit
	}
	if config.Cfg.MaxQueryLimit > 0 && (limit > config.Cfg.MaxQueryLimit || limit == 0) {
		limit = config.Cfg.MaxQueryLimit
	}

	offset := 0
	if query.Offset != nil && *query.Offset >= 0 {
		offset = *query.Offset
	}

	if limit > 0 {
		baseQuery += fmt.Sprintf("LIMIT %d ", limit)
	}
	if offset > 0 {
		baseQuery += fmt.Sprintf("OFFSET %d ", offset)
	}

	row := exec.QueryRowContext(ctx, fmt.Sprintf("SELECT json_group_array(%s) AS data FROM (%s)", agg, baseQuery), args...)
	if err := row.Scan(&result.Data); err != nil {
		return SelectResult{}, err
	}

	return result, nil
}

// InsertJSON inserts a single row using JSON body format.
// POST /data/query/{table} (no Prefer header)
func (dao *TenantConnection) InsertJSON(ctx context.Context, relation string, req InsertRequest) ([]byte, error) {
	return dao.insertJSON(ctx, dao.Client, relation, req)
}

func (dao *TenantConnection) insertJSON(ctx context.Context, exec Executor, relation string, req InsertRequest) ([]byte, error) {
	if err := tools.ValidateTableName(relation); err != nil {
		return nil, err
	}

	table, err := dao.Schema.SearchTbls(relation)
	if err != nil {
		return nil, err
	}

	if len(req.Data) == 0 {
		return nil, errors.New("insert requires at least one row")
	}

	if len(req.Data[0]) == 0 {
		return nil, errors.New("insert rows must have at least one column")
	}

	// Build column list from first row - collect into slice for consistent ordering
	columns := make([]string, 0, len(req.Data[0]))
	for col := range req.Data[0] {
		if _, err := table.SearchCols(col); err != nil {
			return nil, err
		}
		columns = append(columns, col)
	}

	query := fmt.Sprintf("INSERT INTO [%s] ( ", relation)
	args := make([]any, 0, len(req.Data)*len(columns))
	valuesTemplate := "( "

	for _, col := range columns {
		query += fmt.Sprintf("[%s], ", col)
		valuesTemplate += "?, "
	}

	query = query[:len(query)-2] + " ) VALUES "
	valuesTemplate = valuesTemplate[:len(valuesTemplate)-2] + " )"

	// Build values for each row using consistent column order
	for i, row := range req.Data {
		if i > 0 {
			query += ", "
		}
		query += valuesTemplate

		for _, col := range columns {
			args = append(args, row[col])
		}
	}

	query += " "

	if len(req.Returning) > 0 {
		retQuery, err := table.BuildReturningFromJSON(req.Returning)
		if err != nil {
			return nil, err
		}
		query += retQuery
		return dao.queryJSONWithExec(ctx, exec, query, args...)
	}

	result, err := ExecContextWithRetry(ctx, exec, query, args...)
	if err != nil {
		return nil, err
	}

	lastInsertId, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}
	return json.Marshal(map[string]any{"last_insert_id": lastInsertId})
}

// InsertIgnoreJSON inserts row(s), ignoring conflicts.
// POST /data/query/{table} with Prefer: operation=insert,on-conflict=ignore
func (dao *TenantConnection) InsertIgnoreJSON(ctx context.Context, relation string, req InsertRequest) ([]byte, error) {
	return dao.insertIgnoreJSON(ctx, dao.Client, relation, req)
}

func (dao *TenantConnection) insertIgnoreJSON(ctx context.Context, exec Executor, relation string, req InsertRequest) ([]byte, error) {
	if err := tools.ValidateTableName(relation); err != nil {
		return nil, err
	}

	table, err := dao.Schema.SearchTbls(relation)
	if err != nil {
		return nil, err
	}

	if len(req.Data) == 0 {
		return nil, errors.New("insert requires at least one row")
	}

	if len(req.Data[0]) == 0 {
		return nil, errors.New("insert rows must have at least one column")
	}

	// Build column list from first row - collect into slice for consistent ordering
	columns := make([]string, 0, len(req.Data[0]))
	for col := range req.Data[0] {
		if _, err := table.SearchCols(col); err != nil {
			return nil, err
		}
		columns = append(columns, col)
	}

	query := fmt.Sprintf("INSERT OR IGNORE INTO [%s] ( ", relation)
	args := make([]any, 0, len(req.Data)*len(columns))
	valuesTemplate := "( "

	for _, col := range columns {
		query += fmt.Sprintf("[%s], ", col)
		valuesTemplate += "?, "
	}

	query = query[:len(query)-2] + " ) VALUES "
	valuesTemplate = valuesTemplate[:len(valuesTemplate)-2] + " )"

	// Build values for each row using consistent column order
	for i, row := range req.Data {
		if i > 0 {
			query += ", "
		}
		query += valuesTemplate

		for _, col := range columns {
			args = append(args, row[col])
		}
	}

	query += " "

	if len(req.Returning) > 0 {
		retQuery, err := table.BuildReturningFromJSON(req.Returning)
		if err != nil {
			return nil, err
		}
		query += retQuery
		return dao.queryJSONWithExec(ctx, exec, query, args...)
	}

	result, err := ExecContextWithRetry(ctx, exec, query, args...)
	if err != nil {
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	return json.Marshal(map[string]any{"rows_affected": rowsAffected})
}

// UpsertJSON inserts multiple rows, updating on conflict.
// POST /data/query/{table} with Prefer: on-conflict=replace
func (dao *TenantConnection) UpsertJSON(ctx context.Context, relation string, req UpsertRequest) ([]byte, error) {
	return dao.upsertJSON(ctx, dao.Client, relation, req)
}

func (dao *TenantConnection) upsertJSON(ctx context.Context, exec Executor, relation string, req UpsertRequest) ([]byte, error) {
	if err := tools.ValidateTableName(relation); err != nil {
		return nil, err
	}

	table, err := dao.Schema.SearchTbls(relation)
	if err != nil {
		return nil, err
	}

	if len(req.Data) == 0 {
		return nil, errors.New("upsert requires at least one row")
	}

	if len(req.Data[0]) == 0 {
		return nil, errors.New("upsert rows must have at least one column")
	}

	// Collect columns into slice for consistent ordering
	columns := make([]string, 0, len(req.Data[0]))
	for col := range req.Data[0] {
		if _, err := table.SearchCols(col); err != nil {
			return nil, err
		}
		columns = append(columns, col)
	}

	query := fmt.Sprintf("INSERT INTO [%s] ( ", relation)
	args := make([]any, 0, len(req.Data)*len(columns))
	valuesTemplate := "( "

	for _, col := range columns {
		query += fmt.Sprintf("[%s], ", col)
		valuesTemplate += "?, "
	}

	valuesTemplate = valuesTemplate[:len(valuesTemplate)-2] + " )"
	query = query[:len(query)-2] + " ) VALUES "

	// Build values for each row
	for i, row := range req.Data {
		if i > 0 {
			query += ", "
		}
		query += valuesTemplate

		for _, col := range columns {
			args = append(args, row[col])
		}
	}

	if len(table.Pk) == 0 {
		query += " ON CONFLICT(rowid) DO UPDATE SET "
	} else {
		pkCols := make([]string, len(table.Pk))
		for i, col := range table.Pk {
			pkCols[i] = fmt.Sprintf("[%s]", col)
		}
		query += fmt.Sprintf(" ON CONFLICT(%s) DO UPDATE SET ", strings.Join(pkCols, ", "))
	}

	for _, col := range columns {
		query += fmt.Sprintf("[%s] = excluded.[%s], ", col, col)
	}

	query = query[:len(query)-2] + " "

	if len(req.Returning) > 0 {
		retQuery, err := table.BuildReturningFromJSON(req.Returning)
		if err != nil {
			return nil, err
		}
		query += retQuery
		return dao.queryJSONWithExec(ctx, exec, query, args...)
	}

	result, err := ExecContextWithRetry(ctx, exec, query, args...)
	if err != nil {
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	return json.Marshal(map[string]any{"rows_affected": rowsAffected})
}

// UpdateJSON modifies rows using JSON body format.
// PATCH /data/query/{table}
func (dao *TenantConnection) UpdateJSON(ctx context.Context, relation string, req UpdateRequest) ([]byte, error) {
	return dao.updateJSON(ctx, dao.Client, relation, req)
}

func (dao *TenantConnection) updateJSON(ctx context.Context, exec Executor, relation string, req UpdateRequest) ([]byte, error) {
	if err := tools.ValidateTableName(relation); err != nil {
		return nil, err
	}

	table, err := dao.Schema.SearchTbls(relation)
	if err != nil {
		return nil, err
	}

	if len(req.Data) == 0 {
		return nil, errors.New("update requires at least one column")
	}

	query := fmt.Sprintf("UPDATE [%s] SET ", relation)
	var args []any

	first := true
	for col, val := range req.Data {
		_, err = table.SearchCols(col)
		if err != nil {
			return nil, err
		}

		if !first {
			query += ", "
		}
		first = false
		query += fmt.Sprintf("[%s] = ?", col)
		args = append(args, val)
	}
	query += " "

	where, whereArgs, err := table.BuildWhereFromJSON(req.Where, dao.Schema)
	if err != nil {
		return nil, err
	}

	if where == "" {
		return nil, tools.ErrMissingWhereClause
	}
	query += where
	args = append(args, whereArgs...)

	result, err := ExecContextWithRetry(ctx, exec, query, args...)
	if err != nil {
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	return json.Marshal(map[string]any{"rows_affected": rowsAffected})
}

// DeleteJSON removes rows using JSON body format.
// DELETE /data/query/{table}
func (dao *TenantConnection) DeleteJSON(ctx context.Context, relation string, req DeleteRequest) ([]byte, error) {
	return dao.deleteJSON(ctx, dao.Client, relation, req)
}

func (dao *TenantConnection) deleteJSON(ctx context.Context, exec Executor, relation string, req DeleteRequest) ([]byte, error) {
	if err := tools.ValidateTableName(relation); err != nil {
		return nil, err
	}

	table, err := dao.Schema.SearchTbls(relation)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("DELETE FROM [%s] ", relation)

	where, args, err := table.BuildWhereFromJSON(req.Where, dao.Schema)
	if err != nil {
		return nil, err
	}

	if where == "" {
		return nil, tools.ErrMissingWhereClause
	}
	query += where

	result, err := ExecContextWithRetry(ctx, exec, query, args...)
	if err != nil {
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	return json.Marshal(map[string]any{"rows_affected": rowsAffected})
}

// queryJSONWithExec executes a query and returns JSON results using the provided executor.
func (dao *TenantConnection) queryJSONWithExec(ctx context.Context, exec Executor, query string, args ...any) ([]byte, error) {
	rows, err := exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]any

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = values[i]
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return json.Marshal(results)
}
