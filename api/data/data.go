package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/primarystore"
	_ "github.com/mattn/go-sqlite3"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

// NewAPI builds a Data API module using the shared primary metadata store.
func NewAPI(primaryStore *primarystore.Store) (*API, error) {
	if primaryStore == nil || primaryStore.DB() == nil {
		return nil, errors.New("nil primary store")
	}

	// Preload template schemas into cache.
	if err := PreloadSchemaCache(primaryStore.DB()); err != nil {
		// Non-fatal - cache will be populated on demand.
		log.Printf("Warning: failed to preload schema cache: %v", err)
	}

	return &API{store: primaryStore}, nil
}

// connTurso opens a connection to an external Turso database by name.
func (api *API) connTurso(dbName string) (Database, error) {
	org := config.Cfg.TursoOrganization
	token := config.Cfg.TursoGroupAuthToken

	if org == "" {
		return Database{}, errors.New("TURSO_ORGANIZATION environment variable is not set but is required to access external databases")
	}

	if token == "" {
		return Database{}, errors.New("TURSO_GROUP_AUTH_TOKEN environment variable is not set but is required to access external databases")
	}

	if api == nil || api.store == nil || api.store.DB() == nil {
		return Database{}, errors.New("primary store not initialized")
	}

	meta, err := api.store.LookupDatabaseByName(dbName)
	if err != nil {
		return Database{}, err
	}

	// Get cached template (schema + current version).
	schema, currentVersion, err := GetCachedTemplate(api.store.DB(), meta.TemplateID)
	if err != nil {
		return Database{}, fmt.Errorf("failed to load schema: %w", err)
	}

	client, err := sql.Open("libsql", fmt.Sprintf("libsql://%s-%s.turso.io?authToken=%s", dbName, org, token))
	if err != nil {
		return Database{}, err
	}

	err = client.Ping()
	if err != nil {
		client.Close()
		return Database{}, err
	}

	return Database{
		Client:          client,
		Schema:          schema,
		ID:              meta.ID,
		TemplateID:      meta.TemplateID,
		SchemaVersion:   currentVersion,
		DatabaseVersion: meta.DatabaseVersion,
		primaryStore:    api.store,
	}, nil
}

// QueryMap executes a query and returns results as a slice of maps.
func (dao *Database) QueryMap(ctx context.Context, query string, args ...any) ([]interface{}, error) {
	rows, err := dao.Client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columnTypes, err := rows.ColumnTypes()

	if err != nil {
		return nil, err
	}

	count := len(columnTypes)
	finalRows := []interface{}{}

	for rows.Next() {

		scanArgs := make([]interface{}, count)

		for i, v := range columnTypes {

			// doesnt use scanType to support more sqlite drivers
			switch v.DatabaseTypeName() {
			case "TEXT":
				scanArgs[i] = new(sql.NullString)
			case "INTEGER":
				scanArgs[i] = new(sql.NullInt64)
			case "REAL":
				scanArgs[i] = new(sql.NullFloat64)
			case "BLOB":
				scanArgs[i] = new(sql.RawBytes)
			default:
				scanArgs[i] = new(sql.NullString)
			}
		}

		err := rows.Scan(scanArgs...)

		if err != nil {
			return nil, err
		}

		masterData := map[string]interface{}{}

		for i, v := range columnTypes {
			if z, ok := (scanArgs[i]).(*sql.NullString); ok {
				if z.Valid {
					masterData[v.Name()] = z.String
				} else {
					masterData[v.Name()] = nil
				}
				continue
			}

			if z, ok := (scanArgs[i]).(*sql.NullInt64); ok {
				if z.Valid {
					masterData[v.Name()] = z.Int64
				} else {
					masterData[v.Name()] = nil
				}
				continue
			}

			if z, ok := (scanArgs[i]).(*sql.NullFloat64); ok {
				if z.Valid {
					masterData[v.Name()] = z.Float64
				} else {
					masterData[v.Name()] = nil
				}
				continue
			}

			masterData[v.Name()] = scanArgs[i]
		}

		finalRows = append(finalRows, masterData)
	}

	return finalRows, nil
}

// QueryJSON executes a query and returns results as JSON bytes.
func (dao *Database) QueryJSON(ctx context.Context, query string, args ...any) ([]byte, error) {
	m, err := dao.QueryMap(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	return json.Marshal(&m)
}

// LoadSchema deserializes a SchemaCache from gob-encoded bytes.
func LoadSchema(data []byte) (SchemaCache, error) {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var schema SchemaCache

	err := dec.Decode(&schema)

	return schema, err
}
