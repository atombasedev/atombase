package data

import (
	"fmt"
	"strings"

	"github.com/atombasedev/atombase/definitions"
)

func buildInsertSelectSQL(prefix, relation string, columns []string, rows []map[string]any, policy definitions.CompiledPredicate) (string, []any) {
	query := fmt.Sprintf("%s INTO [%s] (", prefix, relation)
	for _, col := range columns {
		query += fmt.Sprintf("[%s], ", col)
	}
	query = query[:len(query)-2] + ") "

	args := make([]any, 0, len(rows)*(len(columns)+len(policy.Args)))
	selects := make([]string, 0, len(rows))
	for _, row := range rows {
		placeholders := strings.TrimRight(strings.Repeat("?, ", len(columns)), ", ")
		selectSQL := "SELECT " + placeholders
		for _, col := range columns {
			args = append(args, row[col])
		}
		if policy.SQL != "" {
			selectSQL += " WHERE " + policy.SQL
			args = append(args, policy.Args...)
		}
		selects = append(selects, selectSQL)
	}

	query += strings.Join(selects, " UNION ALL ") + " "
	return query, args
}
