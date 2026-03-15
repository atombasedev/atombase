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
| `APP_URL` | empty | Optional app URL included in outbound emails |
| `DB_PATH` | `atomicdata/primary.db` | Local primary database path |
| `DATA_DIR` | `atomicdata` | Local data directory |
| `INIT_SCHEMA` | `true` | Initialize the primary schema on startup |
| `ATOMICBASE_REQUEST_TIMEOUT` | `30` | Request timeout in seconds |
| `ATOMICBASE_API_KEY` | empty | Service API key for platform access |
| `ATOMICBASE_CORS_ORIGINS` | empty | Allowed CORS origins |
| `ATOMICBASE_MAX_ORGANIZATIONS_PER_USER` | `3` | Max orgs a session user can own (`0` disables the cap) |
| `ATOMICBASE_TRUSTED_PROXY_CIDRS` | empty | Comma-separated proxy IPs/CIDRs allowed to supply `X-Forwarded-For` |

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

### Email

| Variable | Default | Description |
| --- | --- | --- |
| `SMTP_HOST` | empty | SMTP host for auth and invitation email |
| `SMTP_PORT` | `587` | SMTP port |
| `SMTP_USERNAME` | empty | SMTP username |
| `SMTP_PASSWORD` | empty | SMTP password |
| `SMTP_FROM` | empty | From address for outgoing email |

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

### Auth API

Auth routes accept:

- `Authorization: Bearer <session-id>.<secret>` for member-scoped organization actions
- `Authorization: Bearer service.<ATOMICBASE_API_KEY>` for service-scoped organization actions

For session-backed callers, organization existence is only confirmed to members of that organization. Non-members get the same authorization failure for both missing and real organizations. Service auth can manage organizations directly.

Organization invite creation sends an email to the invited address. If SMTP is not configured, the backend logs the email payload to stdout so local development can still exercise the flow.

User database provisioning is session-backed through `POST /auth/me/database`. That route creates the caller's one allowed user database from a user definition and updates `/auth/me` to include `databaseId`.

Session-backed `POST /auth/orgs` is also capped by `ATOMICBASE_MAX_ORGANIZATIONS_PER_USER`. Service auth bypasses that quota.

### Browser app pattern

The intended browser-app flow is:

1. start magic-link auth
2. complete login and receive a session token
3. persist that token client-side
4. restore it on app boot
5. call `/auth/me`
6. if `databaseId` is missing, call `POST /auth/me/database`
7. call the data API directly from the browser with that session token

For the official browser example path, store the session token in `localStorage`, restore it on startup, and clear it on sign-out. Security comes from session auth plus definition-driven access and provisioning policies, not from proxying requests through a custom app backend.

## Database Targeting

Data requests support these routing modes:

- no `Database` header: use the authenticated user's own database
- `global:<database-id>`
- `org:<organization-id>`

Examples:

```http
Database: global:public-catalog-prod
Database: org:org_123
```

When the header is omitted, the primary database resolves the current session user to their linked user database. If the user does not have one, the request fails. Anonymous and service requests still require an explicit `Database` header.

The primary database resolves that routing input into a concrete tenant database plus definition metadata.

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
    "roles": ["owner", "admin", "member", "viewer"],
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
    },
    "management": {
      "owner": {
        "invite": {"any": true},
        "assignRole": {"any": true},
        "removeMember": {"any": true},
        "updateOrg": true,
        "deleteOrg": true,
        "transferOwnership": true
      },
      "admin": {
        "invite": ["member", "viewer"],
        "assignRole": ["member", "viewer"],
        "removeMember": ["member", "viewer"]
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
- `organization`: one database per organization, managed through the auth API

Organization databases get tenant-local `atombase_membership` storage created during provisioning.

The platform database endpoint no longer provisions organization databases directly. Use `POST /auth/orgs` instead.

## Auth API

### Routes

- `POST /auth/magic-link/start`
- `GET /auth/magic-link/complete`
- `POST /auth/signout`
- `GET /auth/me`
- `GET /auth/orgs`
- `POST /auth/orgs`
- `GET /auth/orgs/{orgID}`
- `GET /auth/orgs/{orgID}/members`
- `POST /auth/orgs/{orgID}/members`
- `PATCH /auth/orgs/{orgID}/members/{userID}`
- `DELETE /auth/orgs/{orgID}/members/{userID}`
- `PATCH /auth/orgs/{orgID}`
- `DELETE /auth/orgs/{orgID}`
- `POST /auth/orgs/{orgID}/transfer-ownership`

### Create Organization

Organization creation always provisions a backing organization database from the supplied organization definition.

```bash
curl -X POST http://localhost:8080/auth/orgs \
  -H "Authorization: Bearer session.secret" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "org_123",
    "name": "Acme",
    "definition": "workspace",
    "maxMembers": 250,
    "metadata": {"region": "na"}
  }'
```

Service callers may also provide `ownerId`. Session callers always create organizations owned by themselves.

### List / Get Organizations

`GET /auth/orgs` returns all organizations for service auth, and only organizations where the session user is an active member for session auth.

`GET /auth/orgs/{orgID}` only confirms organization existence for service callers and active members.

### Membership Management

Membership actions are driven by the definition's `management` policy, not hardcoded role names.

```bash
curl -X POST http://localhost:8080/auth/orgs/org_123/members \
  -H "Authorization: Bearer session.secret" \
  -H "Content-Type: application/json" \
  -d '{
    "userId": "user_2",
    "role": "viewer",
    "status": "active"
  }'
```

### Update Organization

```bash
curl -X PATCH http://localhost:8080/auth/orgs/org_123 \
  -H "Authorization: Bearer session.secret" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Acme North America",
    "metadata": {"region": "na"},
    "maxMembers": 250
  }'
```

### Transfer Ownership

```bash
curl -X POST http://localhost:8080/auth/orgs/org_123/transfer-ownership \
  -H "Authorization: Bearer session.secret" \
  -H "Content-Type: application/json" \
  -d '{
    "userId": "user_2"
  }'
```

Transfer ownership updates both primary organization ownership and tenant-local owner membership.

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
- definitions also drive org management permissions through stored `management` policies
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
- auth API with definition-driven org management and ownership transfer
- lazy migrations plus definition version history
- local and first-tenant probes during definition pushes

## Current Limitations

- batch support still exists but is not the long-term preferred API
- migration validation does not yet seed local probe databases with representative data
- SQLite constraints still apply for write concurrency and some schema changes

## Operational Notes

- `GET /health` is available without auth
- `GET /docs` serves Swagger UI
- request logging, activity logging, and cache backends are configurable
- production deployments should set `ATOMICBASE_API_KEY`, `TOKEN_ENCRYPTION_KEY`, and durable storage explicitly
