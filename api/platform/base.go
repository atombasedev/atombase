package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/atombasedev/atombase/primarystore"
)

// API is the Platform API module with injected dependencies.
type API struct {
	store *primarystore.Store
}

// Table names for internal platform tables.
const (
	TableTemplates         = "atombase_schema_templates"
	TableTemplatesHistory  = "atombase_templates_history"
	TableDatabases         = "atombase_databases"
	TableMigrations        = "atombase_migrations"
	TableMigrationFailures = "atombase_migration_failures"
)

// NewAPI builds a Platform API module using the shared primary metadata store.
func NewAPI(primaryStore *primarystore.Store) (*API, error) {
	if primaryStore == nil || primaryStore.DB() == nil {
		return nil, errors.New("nil primary store")
	}
	return &API{store: primaryStore}, nil
}

func (api *API) dbConn() (*sql.DB, error) {
	if api == nil || api.store == nil || api.store.DB() == nil {
		return nil, errors.New("platform database not initialized")
	}
	return api.store.DB(), nil
}

// queryJSON executes a query and returns results as JSON bytes.
func (api *API) queryJSON(ctx context.Context, query string, args ...any) ([]byte, error) {
	conn, err := api.dbConn()
	if err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(ctx, query, args...)
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
