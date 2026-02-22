package data

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/atomicbase/atomicbase/config"
	"github.com/atomicbase/atomicbase/tools"
)

// //go:embed openapi.yaml
// var openapiSpec []byte

// DbHandler is a handler that operates on a Database.
type DbHandler func(ctx context.Context, db *Database, req *http.Request) ([]byte, error)

// DbResponseHandler is a handler that needs access to the ResponseWriter.
type DbResponseHandler func(ctx context.Context, db *Database, req *http.Request, w http.ResponseWriter) ([]byte, error)

// RegisterRoutes registers all Data API routes on the provided ServeMux.
// All routes are prefixed with /data.
func RegisterRoutes(app *http.ServeMux) {
	// Health check (no auth required)
	app.HandleFunc("GET /health", handleHealth())
	// OpenAPI documentation (no auth required)
	// app.HandleFunc("GET /openapi.yaml", handleOpenAPISpec())
	app.HandleFunc("GET /docs", handleSwaggerUI())

	// Data API routes
	app.HandleFunc("POST /data/query/{table}", handleQueryRows())
	app.HandleFunc("POST /data/batch", handleBatch())
}

// withDB wraps handlers that can use either the primary or an external database.
func withDB(handler DbHandler) http.HandlerFunc {
	return func(wr http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		req.Body = http.MaxBytesReader(wr, req.Body, config.Cfg.MaxRequestBody)
		defer req.Body.Close()

		dao, isExternal, err := connDb(req)
		if err != nil {
			tools.RespErr(wr, err)
			return
		}
		// Only close external (non-pooled) connections
		if isExternal {
			defer dao.Client.Close()
		}

		if err := MigrateIfNeeded(ctx, &dao); err != nil {
			respondMigrationFailed(wr, err)
			return
		}

		data, err := handler(ctx, &dao, req)
		if err != nil {
			tools.RespErr(wr, err)
			return
		}

		if data != nil {
			wr.Header().Set("Content-Type", "application/json")
		}
		wr.Write(data)
	}
}

// withDBResponse wraps handlers that need access to the ResponseWriter for setting headers.
func withDBResponse(handler DbResponseHandler) http.HandlerFunc {
	return func(wr http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		req.Body = http.MaxBytesReader(wr, req.Body, config.Cfg.MaxRequestBody)
		defer req.Body.Close()

		dao, isExternal, err := connDb(req)
		if err != nil {
			tools.RespErr(wr, err)
			return
		}
		// Only close external (non-pooled) connections
		if isExternal {
			defer dao.Client.Close()
		}

		if err := MigrateIfNeeded(ctx, &dao); err != nil {
			respondMigrationFailed(wr, err)
			return
		}

		data, err := handler(ctx, &dao, req, wr)
		if err != nil {
			tools.RespErr(wr, err)
			return
		}

		if data != nil {
			wr.Header().Set("Content-Type", "application/json")
		}
		wr.Write(data)
	}
}

// connDb returns a database connection for the specified database.
// The Database header is required - the internal primary database cannot be queried directly.
// Returns the connection and a boolean indicating if it should be closed after use.
func connDb(req *http.Request) (Database, bool, error) {
	dbName := req.Header.Get("Database")
	if dbName == "" {
		return Database{}, false, tools.ErrMissingDatabase
	}

	dao, err := ConnPrimary()
	if err != nil {
		return Database{}, false, err
	}

	db, err := dao.ConnTurso(dbName)
	if err != nil {
		return Database{}, false, err
	}

	return db, true, nil
}

// handleBatch handles POST /data/batch for atomic multi-operation requests.
func handleBatch() http.HandlerFunc {
	return withDB(func(ctx context.Context, dao *Database, req *http.Request) ([]byte, error) {
		var batchReq BatchRequest
		if err := json.NewDecoder(req.Body).Decode(&batchReq); err != nil {
			return nil, err
		}
		result, err := dao.Batch(ctx, batchReq)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
}

// handleQueryRows handles POST /data/query/{table} for SELECT, INSERT, UPDATE, and DELETE operations.
func handleQueryRows() http.HandlerFunc {
	return withDBResponse(func(ctx context.Context, dao *Database, req *http.Request, w http.ResponseWriter) ([]byte, error) {
		table := req.PathValue("table")

		operation, onConflict, countExact := parsePreferHeaders(req)

		switch operation {
		case "select":
			{
				var query SelectQuery
				if err := json.NewDecoder(req.Body).Decode(&query); err != nil {
					return nil, err
				}

				result, err := dao.SelectJSON(ctx, table, query, countExact)
				if err != nil {
					return nil, err
				}

				if countExact {
					w.Header().Set("X-Total-Count", strconv.FormatInt(result.Count, 10))
				}

				return result.Data, nil
			}
		case "insert":
			{
				if onConflict == "" {
					var insertReq InsertRequest
					if err := json.NewDecoder(req.Body).Decode(&insertReq); err != nil {
						return nil, err
					}
					return dao.InsertJSON(ctx, table, insertReq)
				}
				if onConflict == "replace" {
					var upsertReq UpsertRequest
					if err := json.NewDecoder(req.Body).Decode(&upsertReq); err != nil {
						return nil, err
					}
					return dao.UpsertJSON(ctx, table, upsertReq)
				}
				if onConflict == "ignore" {
					var ignoreReq InsertRequest
					if err := json.NewDecoder(req.Body).Decode(&ignoreReq); err != nil {
						return nil, err
					}
					return dao.InsertIgnoreJSON(ctx, table, ignoreReq)
				}
				return nil, tools.ErrInvalidOnConflict
			}
		case "update":
			{
				var updateReq UpdateRequest
				if err := json.NewDecoder(req.Body).Decode(&updateReq); err != nil {
					return nil, err
				}
				return dao.UpdateJSON(ctx, table, updateReq)
			}
		case "delete":
			{
				var deleteReq DeleteRequest
				if err := json.NewDecoder(req.Body).Decode(&deleteReq); err != nil {
					return nil, err
				}
				return dao.DeleteJSON(ctx, table, deleteReq)
			}
		}

		return nil, tools.ErrMissingOperation
	})
}

func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check database connectivity using the connection pool
		dao, err := ConnPrimary()
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","error":"database connection failed"}`))
			return
		}
		// Note: don't close dao.Client - it's managed by the connection pool

		if err := dao.Client.PingContext(r.Context()); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","error":"database ping failed"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	}
}

// func handleOpenAPISpec() http.HandlerFunc {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		w.Header().Set("Content-Type", "application/x-yaml")
// 		w.Header().Set("Access-Control-Allow-Origin", "*")
// 		w.Write(openapiSpec)
// 	}
// }

func handleSwaggerUI() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
  <title>Atomicbase API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: '/openapi.yaml',
      dom_id: '#swagger-ui',
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      layout: 'BaseLayout'
    });
  </script>
</body>
</html>`))
	}
}

func parsePreferHeaders(req *http.Request) (operation string, onConflict string, countExact bool) {
	vals := tools.ParseHeaderCommas(req.Header.Values("Prefer"))

	for _, v := range vals {
		normalized := strings.ToLower(strings.ReplaceAll(v, " ", ""))
		if strings.HasPrefix(normalized, "operation=") {
			operation, _ = strings.CutPrefix(normalized, "operation=")
			continue
		}
		if strings.HasPrefix(normalized, "on-conflict=") {
			onConflict, _ = strings.CutPrefix(normalized, "on-conflict=")
			continue
		}
		if normalized == "count=exact" {
			countExact = true
		}
	}

	return operation, onConflict, countExact
}

func respondMigrationFailed(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(tools.APIError{
		Code:    "MIGRATION_FAILED",
		Message: "Database migration failed. Please try again.",
		Hint:    "If this persists, contact support. Error: " + err.Error(),
	})
}
