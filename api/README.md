# Atomicbase API

The Go backend for Atomicbase. It serves a definitions-first multi-database API on top of SQLite and Turso.

## Overview

Atomicbase exposes two HTTP surfaces:

- `Data API` at `/data/*` for tenant-scoped CRUD and query execution
- `Platform API` at `/platform/*` for definition storage, versioning, and database provisioning

The primary database stores identity, definitions, database routing metadata, access policies, and migration history. Tenant databases store application data. For organization databases, tenant-local membership is the source of truth for authorization.

## Getting Started

### Prerequisites

- Go 1.25+
- CGO enabled
- a C toolchain for `github.com/mattn/go-sqlite3`

### Build

```bash
CGO_ENABLED=1 go build -tags fts5 -o bin/atomicbase
```

### Run

```bash
./bin/atomicbase
```

The server listens on `http://localhost:8080` by default.

### Test

```bash
CGO_ENABLED=1 go test -tags fts5 ./...
```

## Configuration

### Core

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `:8080` | HTTP server port |
| `API_URL` | `http://localhost:8080` | Base API URL |
| `DB_PATH` | `atomicdata/primary.db` | Local primary database path |
| `DATA_DIR` | `atomicdata` | Local data directory |
| `INIT_SCHEMA` | `true` | Initialize the primary schema on startup |
| `ATOMICBASE_REQUEST_TIMEOUT` | `30` | Request timeout in seconds |
| `ATOMICBASE_API_KEY` | empty | Service API key for platform access |
| `ATOMICBASE_CORS_ORIGINS` | empty | Allowed CORS origins |

### Query Limits

| Variable | Default | Description |
| --- | --- | --- |
| `ATOMICBASE_MAX_QUERY_DEPTH` | `5` | Max nested relation depth |
| `ATOMICBASE_MAX_QUERY_LIMIT` | `1000` | Max rows per query |
| `ATOMICBASE_DEFAULT_LIMIT` | `100` | Default row limit |

### Turso

| Variable | Default | Description |
| --- | --- | --- |
| `TURSO_ORGANIZATION` | empty | Turso organization |
| `TURSO_API_KEY` | empty | Turso management API key |
| `TURSO_GROUP` | `default` | Turso group |
| `PRIMARY_DB_NAME` | empty | Use Turso for the primary database when set |
| `PRIMARY_DB_TOKEN` | empty | Auth token for the primary Turso database |
| `TOKEN_ENCRYPTION_KEY` | empty | Required when `TURSO_ORGANIZATION` is set |

### Cache and Logging

| Variable | Default | Description |
| --- | --- | --- |
| `CACHE_REDIS_URL` | empty | Redis cache URL |
| `CACHE_REDIS_PASSWORD` | empty | Redis cache password |
| `CACHE_SQLITE_PATH` | empty | SQLite-backed cache path |
| `CACHE_KEY_PREFIX` | empty | Cache key prefix |
| `ATOMICBASE_ACTIVITY_LOG_ENABLED` | `false` | Enable activity logging |
| `ATOMICBASE_ACTIVITY_LOG_PATH` | `atomicdata/logs.db` | Activity log DB path |
| `ATOMICBASE_ACTIVITY_LOG_RETENTION` | `30` | Activity log retention in days |

## Authentication

### Platform API

Platform routes require a service bearer token:

```http
Authorization: Bearer service.<ATOMICBASE_API_KEY>
```

### Data API

Data routes accept:

- no `Authorization` header for anonymous requests
- `Authorization: Bearer service.<ATOMICBASE_API_KEY>` for service access
- `Authorization: Bearer <session-id>.<secret>` for session-backed user access

The auth middleware injects only the caller identity. Definitions and tenant-local policies decide what the caller can do.

## Database Targeting

Every data request must include a `Database` header. The value is a routed target:

- `global:<database-id>`
- `user:<definition-name>`
- `org:<organization-id>`

Examples:

```http
Database: global:public-catalog-prod
Database: user:notes
Database: org:org_123
```

The primary database resolves that header into a concrete tenant database plus definition metadata.

## Data API

### Routes

- `POST /data/query/{table}`
- `POST /data/batch`
- `GET /docs`

All query operations use `POST /data/query/{table}` with the `Prefer` header.

### Prefer Header

Supported values:

- `operation=select`
- `operation=insert`
- `operation=update`
- `operation=delete`
- `on-conflict=replace`
- `on-conflict=ignore`
- `count=exact`

Example:

```http
Prefer: operation=insert, on-conflict=replace
```

### Select

```bash
curl -X POST http://localhost:8080/data/query/projects \
  -H "Database: org:org_123" \
  -H "Prefer: operation=select, count=exact" \
  -H "Content-Type: application/json" \
  -d '{
    "select": ["id", "name", {"owner": ["id", "name"]}],
    "where": [{"status": {"eq": "active"}}],
    "order": {"created_at": "desc"},
    "limit": 20
  }'
```

### Insert

```bash
curl -X POST http://localhost:8080/data/query/projects \
  -H "Database: org:org_123" \
  -H "Prefer: operation=insert" \
  -H "Content-Type: application/json" \
  -d '{
    "data": {"name": "Roadmap", "status": "active"},
    "returning": ["id", "name", "status"]
  }'
```

### Upsert

```bash
curl -X POST http://localhost:8080/data/query/projects \
  -H "Database: org:org_123" \
  -H "Prefer: operation=insert, on-conflict=replace" \
  -H "Content-Type: application/json" \
  -d '{
    "data": {"id": 1, "name": "Roadmap", "status": "active"},
    "returning": ["id", "name"]
  }'
```

### Update

```bash
curl -X POST http://localhost:8080/data/query/projects \
  -H "Database: org:org_123" \
  -H "Prefer: operation=update" \
  -H "Content-Type: application/json" \
  -d '{
    "data": {"status": "archived"},
    "where": [{"id": {"eq": 1}}]
  }'
```

### Delete

```bash
curl -X POST http://localhost:8080/data/query/projects \
  -H "Database: org:org_123" \
  -H "Prefer: operation=delete" \
  -H "Content-Type: application/json" \
  -d '{
    "where": [{"id": {"eq": 1}}]
  }'
```

### Batch

Batch requests are still supported, but they are not the long-term primary API shape.

```bash
curl -X POST http://localhost:8080/data/batch \
  -H "Database: org:org_123" \
  -H "Content-Type: application/json" \
  -d '{
    "operations": [
      {
        "operation": "insert",
        "table": "projects",
        "body": {"data": {"name": "Alpha"}}
      },
      {
        "operation": "update",
        "table": "projects",
        "body": {"data": {"status": "active"}, "where": [{"id": {"eq": 1}}]}
      }
    ]
  }'
```

### Query Notes

- `where` is an array of filter objects
- nested relation selects are resolved from foreign keys
- `count=exact` returns `X-Total-Count`
- definitions policies are compiled into the tenant query path before execution
- lazy migrations run before normal query execution when a tenant database is behind its definition version

## Platform API

### Routes

- `GET /platform/definitions`
- `GET /platform/definitions/{name}`
- `POST /platform/definitions`
- `POST /platform/definitions/{name}/push`
- `GET /platform/definitions/{name}/history`
- `GET /platform/databases`
- `GET /platform/databases/{id}`
- `POST /platform/databases`
- `DELETE /platform/databases/{id}`

### Create Definition

```bash
curl -X POST http://localhost:8080/platform/definitions \
  -H "Authorization: Bearer service.dev-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "workspace",
    "type": "organization",
    "roles": ["owner", "member"],
    "schema": {
      "tables": [
        {
          "name": "projects",
          "pk": ["id"],
          "columns": {
            "id": {"name": "id", "type": "INTEGER"},
            "name": {"name": "name", "type": "TEXT", "notNull": true},
            "owner_id": {"name": "owner_id", "type": "TEXT"}
          }
        }
      ]
    },
    "access": {
      "projects": {
        "select": {"field": "auth.status", "op": "eq", "value": "member"},
        "update": {"field": "auth.role", "op": "eq", "value": "owner"}
      }
    }
  }'
```

### Push Definition Version

```bash
curl -X POST http://localhost:8080/platform/definitions/workspace/push \
  -H "Authorization: Bearer service.dev-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "schema": {
      "tables": [
        {
          "name": "projects",
          "pk": ["id"],
          "columns": {
            "id": {"name": "id", "type": "INTEGER"},
            "name": {"name": "name", "type": "TEXT", "notNull": true},
            "description": {"name": "description", "type": "TEXT"}
          }
        }
      ]
    },
    "access": {
      "projects": {
        "select": {"field": "auth.status", "op": "eq", "value": "member"}
      }
    },
    "merge": []
  }'
```

Definition pushes:

- diff the current and next schema
- generate migration SQL
- run a local in-memory migration probe
- probe the first existing tenant database before publish
- store migration rows in the primary database

### Create Database

```bash
curl -X POST http://localhost:8080/platform/databases \
  -H "Authorization: Bearer service.dev-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "workspace-acme",
    "definition": "workspace",
    "organizationId": "org_123",
    "organizationName": "Acme",
    "ownerId": "user_1"
  }'
```

Definition type determines provisioning semantics:

- `global`: shared database per definition
- `user`: one database per user
- `organization`: one database per organization

Organization databases get tenant-local `atombase_membership` storage created during provisioning.

## Architecture

```text
api/
├── main.go
├── config/
├── data/
│   ├── handlers.go
│   ├── queries.go
│   ├── build_query.go
│   ├── schema_cache.go
│   └── migration.go
├── definitions/
│   ├── service.go
│   ├── compiler.go
│   └── types.go
├── platform/
│   ├── handlers.go
│   ├── definitions.go
│   ├── databases.go
│   ├── migrations.go
│   └── validation.go
├── primarystore/
└── tools/
```

### Runtime Model

- primary auth resolves who the caller is
- primary metadata resolves which tenant database and definition apply
- definitions compile access policies into the tenant SQL path
- organization membership is enforced inside tenant SQL, not through a separate primary lookup
- each tenant request is treated as a single billed request target

### Storage Model

- `primary database`: users, sessions, definitions, definition history, access policies, database registry, migration rows
- `tenant databases`: business tables and tenant-owned authorization state
- `organization tenants`: tenant-local `atombase_membership`

## Current Strengths

- definitions-first runtime and storage model
- per-tenant isolation with Turso-backed databases
- policy-aware data API with tenant-side org membership enforcement
- lazy migrations plus definition version history
- local and first-tenant probes during definition pushes

## Current Limitations

- batch support still exists but is not the long-term preferred API
- migration validation does not yet seed local probe databases with representative data
- organization membership management APIs are not built yet
- SQLite constraints still apply for write concurrency and some schema changes

## Operational Notes

- `GET /health` is available without auth
- `GET /docs` serves Swagger UI
- request logging, activity logging, and cache backends are configurable
- production deployments should set `ATOMICBASE_API_KEY`, `TOKEN_ENCRYPTION_KEY`, and durable storage explicitly
