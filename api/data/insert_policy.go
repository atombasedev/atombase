package data

import (
	"fmt"
	"strings"

	"github.com/atombasedev/atombase/definitions"
)

func buildInsertSourceSQL(columns []string, rows []map[string]any) (string, []any) {
	args := make([]any, 0, len(rows)*len(columns))
	valueRows := make([]string, 0, len(rows))
	for _, row := range rows {
		selectParts := make([]string, 0, len(columns))
		for _, col := range columns {
			args = append(args, row[col])
			selectParts = append(selectParts, fmt.Sprintf("? AS [%s]", col))
		}
		valueRows = append(valueRows, "SELECT "+strings.Join(selectParts, ", "))
	}
	return "(" + strings.Join(valueRows, " UNION ALL ") + ") AS __ab_new", args
}

func buildInsertSelectSQL(prefix, relation string, columns []string, rows []map[string]any, policy definitions.CompiledPredicate) (string, []any) {
	query := fmt.Sprintf("%s INTO [%s] (", prefix, relation)
	for _, col := range columns {
		query += fmt.Sprintf("[%s], ", col)
	}
	query = query[:len(query)-2] + ") "

	sourceSQL, args := buildInsertSourceSQL(columns, rows)
	query += "SELECT "
	for _, col := range columns {
		query += fmt.Sprintf("__ab_new.[%s], ", col)
	}
	query = query[:len(query)-2] + " FROM " + sourceSQL + " "
	if policy.SQL != "" {
		query += "WHERE " + policy.SQL + " "
		args = append(args, policy.Args...)
	} else {
		query += "WHERE 1=1 "
	}
	return query, args
}
