# Atomicbase API

The Go backend server for Atomicbase. A multi-database REST API built on SQLite and Turso.

## Overview

The Atomicbase API provides two main interfaces:

- **Data API** (`/data/*`) - CRUD operations, batch transactions, and schema introspection for target databases
- **Platform API** (`/platform/*`) - Database and template management

The server is packaged as a single Go executable with SQLite embedded. For multi-database deployments, it integrates with Turso for remote database hosting.

## Getting Started

### Prerequisites

- Go 1.25+
- CGO enabled (required for SQLite)
- Make (optional, for convenience commands)

### Build

```bash
# Build with FTS5 support (required)
CGO_ENABLED=1 go build -tags fts5 -o bin/atomicbase

# Or use Make
make build
```

### Configure

Create a `.env` file or set environment variables:

```ini
# Server
PORT=:8080

# Authentication (empty = disabled)
ATOMICBASE_API_KEY=your-secret-key

# CORS (empty = disabled, * = allow all)
ATOMICBASE_CORS_ORIGINS=http://localhost:3000,http://localhost:5173

# For multi-database Turso deployments
TURSO_API_KEY=your-turso-key
TURSO_ORGANIZATION=your-org
```

See [Configuration Reference](#configuration-reference) for all options.

### Run

```bash
# Direct
./bin/atomicbase

# Or with Make
make run

```

Server starts at `http://localhost:8080` by default.

### Test

```bash
# All tests
CGO_ENABLED=1 go test -tags fts5 -v ./...

# Specific package
CGO_ENABLED=1 go test -tags fts5 -v ./data/...

# Or use Make
make test
```

## API Reference

### Data API

All CRUD operations use a single endpoint with the `Prefer` header specifying the operation:

```
POST /data/query/{table}
```

#### Headers

| Header | Description |
|--------|-------------|
| `Authorization` | `Bearer <api-key>` (if auth enabled) |
| `Database` | Target database name |
| `Prefer` | Operation type and options |

#### Operations

**Select**
```bash
curl -X POST http://localhost:8080/data/query/users \
  -H "Database: acme-corp" \
  -H "Prefer: operation=select" \
  -H "Content-Type: application/json" \
  -d '{"select": ["id", "name", "email"], "where": {"id": {"eq": 1}}}'
```

**Insert**
```bash
curl -X POST http://localhost:8080/data/query/users \
  -H "Database: acme-corp" \
  -H "Prefer: operation=insert" \
  -H "Content-Type: application/json" \
  -d '{"values": {"name": "Alice", "email": "alice@example.com"}}'
```

**Upsert** (insert or replace on conflict)
```bash
curl -X POST http://localhost:8080/data/query/users \
  -H "Database: acme-corp" \
  -H "Prefer: operation=insert, on-conflict=replace" \
  -H "Content-Type: application/json" \
  -d '{"values": {"id": 1, "name": "Alice Updated"}}'
```

**Update** (requires where clause)
```bash
curl -X POST http://localhost:8080/data/query/users \
  -H "Database: acme-corp" \
  -H "Prefer: operation=update" \
  -H "Content-Type: application/json" \
  -d '{"set": {"name": "Alicia"}, "where": {"id": {"eq": 1}}}'
```

**Delete** (requires where clause)
```bash
curl -X POST http://localhost:8080/data/query/users \
  -H "Database: acme-corp" \
  -H "Prefer: operation=delete" \
  -H "Content-Type: application/json" \
  -d '{"where": {"id": {"eq": 1}}}'
```

#### Query Features

**Filtering** with operators:
```json
{
  "where": {
    "age": {"gte": 18},
    "status": {"in": ["active", "pending"]},
    "name": {"like": "A%"},
    "or": [
      {"role": {"eq": "admin"}},
      {"role": {"eq": "moderator"}}
    ]
  }
}
```

Supported operators: `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `like`, `glob`, `in`, `between`, `is`, `fts` (full-text search), `and`, `or`, `not`

**Pagination and ordering**:
```json
{
  "select": ["*"],
  "order": [{"column": "created_at", "direction": "desc"}],
  "limit": 20,
  "offset": 0
}
```

**Nested relations** (auto-detected via foreign keys):
```json
{
  "select": ["id", "title", {"author": ["name", "email"]}]
}
```

**Batch operations** (atomic transactions):
```bash
curl -X POST http://localhost:8080/data/batch \
  -H "Database: acme-corp" \
  -H "Content-Type: application/json" \
  -d '{
    "operations": [
      {"table": "accounts", "operation": "update", "set": {"balance": 900}, "where": {"id": {"eq": 1}}},
      {"table": "accounts", "operation": "update", "set": {"balance": 1100}, "where": {"id": {"eq": 2}}}
    ]
  }'
```

### Platform API

#### Templates

Templates define reusable database schemas.

```bash
# List templates
GET /platform/templates

# Get template
GET /platform/templates/{name}

# Create template
POST /platform/templates
{
  "name": "my-app",
  "schema": {
    "tables": [
      {
        "name": "users",
        "pk": ["id"],
        "columns": {
          "id": {"type": "INTEGER"},
          "name": {"type": "TEXT", "notNull": true},
          "email": {"type": "TEXT", "notNull": true, "unique": true}
        }
      }
    ]
  }
}

# Preview schema changes
POST /platform/templates/{name}/diff
{"schema": {/* new schema */}}

# Apply migration
POST /platform/templates/{name}/migrate
{"schema": {/* new schema */}}

# View version history
GET /platform/templates/{name}/history

# Delete template
DELETE /platform/templates/{name}
```

#### Databases

Databases are database instances created from templates.

```bash
# List databases
GET /platform/databases

# Get database
GET /platform/databases/{name}

# Create database
POST /platform/databases
{"name": "acme-corp", "template": "my-app"}

# Sync database to latest template version
POST /platform/databases/{name}/sync

# Delete database
DELETE /platform/databases/{name}
```

### Other Endpoints

```bash
# Health check (no auth required)
GET /health

# OpenAPI spec
GET /openapi.yaml

# Swagger UI (no auth required)
GET /docs
```

## Configuration Reference

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `:8080` | HTTP server port |
| `DB_PATH` | `atomicdata/primary.db` | Primary database path |
| `DATA_DIR` | `atomicdata` | Data directory |
| `ATOMICBASE_REQUEST_TIMEOUT` | `30` | Request timeout in seconds |

### Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `ATOMICBASE_API_KEY` | (empty) | API key for auth. Empty = disabled |

### CORS

| Variable | Default | Description |
|----------|---------|-------------|
| `ATOMICBASE_CORS_ORIGINS` | (empty) | Comma-separated origins. Empty = disabled, `*` = allow all |

### Rate Limiting

| Variable | Default | Description |
|----------|---------|-------------|
| `ATOMICBASE_RATE_LIMIT_ENABLED` | `false` | Enable rate limiting |
| `ATOMICBASE_RATE_LIMIT` | `100` | Requests per minute per IP |

### Query Limits

| Variable | Default | Description |
|----------|---------|-------------|
| `ATOMICBASE_MAX_QUERY_DEPTH` | `5` | Max nesting depth for joins |
| `ATOMICBASE_MAX_QUERY_LIMIT` | `1000` | Max rows per query (0 = unlimited) |
| `ATOMICBASE_DEFAULT_LIMIT` | `100` | Default limit when not specified |

### Turso (Multi-database)

| Variable | Default | Description |
|----------|---------|-------------|
| `TURSO_ORGANIZATION` | (empty) | Turso organization name |
| `TURSO_API_KEY` | (empty) | Turso management API key |
| `TURSO_TOKEN_EXPIRATION` | `7d` | Database token TTL |

### Activity Logging

| Variable | Default | Description |
|----------|---------|-------------|
| `ATOMICBASE_ACTIVITY_LOG_ENABLED` | `false` | Enable request audit logging |
| `ATOMICBASE_ACTIVITY_LOG_PATH` | `atomicdata/logs.db` | Log database path |
| `ATOMICBASE_ACTIVITY_LOG_RETENTION` | `30` | Log retention in days |

## Architecture

```
api/
├── main.go              # Server setup, middleware chain, graceful shutdown
├── config/              # Environment-based configuration
├── data/                # Data API - CRUD, batch, schema introspection
│   ├── handlers.go      # HTTP route handlers
│   ├── queries.go       # SELECT, INSERT, UPDATE, DELETE operations
│   ├── build_query.go   # SQL construction with joins
│   ├── batch.go         # Atomic batch transactions
│   └── schema.go        # Schema introspection
├── platform/            # Platform API - Database management
│   ├── handlers.go      # HTTP route handlers
│   ├── templates.go     # Template CRUD
│   ├── databases.go     # Database management
│   └── migrations.go    # Schema migration planning
└── tools/               # Shared utilities
    ├── middleware.go    # Auth, CORS, rate limit, logging, timeout
    ├── errors.go        # Error codes and types
    └── response.go      # Error response formatting
```

### AI with Tenant Isolation

Every model call is scoped to a tenant. Context includes knowledge bases, database rows, and metadata—all isolated per customer.

No prompt injection across tenants. No data leakage. Just secure, contextual AI for your customers.

```typescript
// ai.ts
const model = ai.model("support-agent");

await model.generate({
  tenantId,
  input,
  context: {
    include: [
      // Tenant-scoped knowledge base
      ai.context.kb("help-center"),

      // Query specific rows
      ai.context.table("customers")
        .where({ id: customerId }),

      // Inject metadata
      ai.context.vars({ plan: "pro" })
    ]
  }
});
```

### Middleware Chain

Requests flow through middleware in this order:

1. **Panic Recovery** - Catches panics, returns 500
2. **Logging** - Structured JSON request logging
3. **Timeout** - Request-scoped context timeout
4. **CORS** - Cross-origin handling
5. **Rate Limit** - Per-IP request throttling
6. **Auth** - API key validation

### Database Architecture

- **Primary Database**: Stores templates, database metadata, and migration history
- **Managed Databases**: Separate SQLite/Turso databases per database
- **Schema Cache**: In-memory cache of table definitions for query validation

## Strengths

- **Simple deployment**: Single binary with embedded SQLite, no external dependencies
- **Database-per-customer isolation**: Each customer gets a separate database with complete isolation
- **Schema versioning**: Templates with version history and declarative migrations
- **Flexible queries**: Complex filtering, joins, pagination, and full-text search
- **Atomic operations**: Batch transactions with all-or-nothing execution
- **Low latency**: Schema caching and connection pooling
- **Turso integration**: Scale to thousands of databases with remote hosting
- **Lazy migrations**: Databases migrate on first request after a template change

## Limitations

- **SQLite constraints**: Single-writer, limited concurrent writes per database
- **No real-time**: Polling required for data changes (websockets planned)
- **No auth module**: Application must handle user authentication (planned)
- **Schema changes**: Some migrations require table recreation (SQLite limitation)
- **CGO dependency**: Requires C compiler for SQLite builds
- **Experimental status**: APIs may change, not recommended for production

## Hosting

### Local Development

Run directly on your machine:

```bash
make run
```

Data stored in `./atomicdata/` by default.

### Docker

```dockerfile
FROM golang:1.21-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY . .

RUN CGO_ENABLED=1 go build -tags fts5 -o atomicbase

FROM alpine:latest
RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /app/atomicbase .

EXPOSE 8080
CMD ["./atomicbase"]
```

```bash
docker build -t atomicbase .
docker run -p 8080:8080 -v $(pwd)/data:/app/atomicdata atomicbase
```

### VPS / Bare Metal

1. Build the binary on a compatible system
2. Copy binary and `.env` to server
3. Run with systemd, supervisor, or similar

Example systemd service:

```ini
[Unit]
Description=Atomicbase API
After=network.target

[Service]
Type=simple
User=atomicbase
WorkingDirectory=/opt/atomicbase
ExecStart=/opt/atomicbase/atomicbase
Restart=always
Environment=PORT=:8080

[Install]
WantedBy=multi-user.target
```

### Cloud Platforms

**Fly.io** (recommended for Turso integration):

```toml
# fly.toml
app = "my-atomicbase"
primary_region = "ord"

[build]

[env]
  PORT = ":8080"

[http_service]
  internal_port = 8080
  force_https = true

[mounts]
  source = "atomicbase_data"
  destination = "/app/atomicdata"
```

```bash
fly launch
fly secrets set ATOMICBASE_API_KEY=your-key TURSO_API_KEY=your-turso-key
fly deploy
```

**Railway / Render**: Similar configuration, ensure persistent volume for SQLite data.

### Production Checklist

- [ ] Set strong `ATOMICBASE_API_KEY`
- [ ] Configure `ATOMICBASE_CORS_ORIGINS` (don't use `*`)
- [ ] Enable rate limiting for public APIs
- [ ] Use Turso for multi-database deployments
- [ ] Set up persistent storage for SQLite
- [ ] Configure reverse proxy (nginx/caddy) with TLS
- [ ] Monitor health endpoint
- [ ] Back up primary database regularly

## Error Handling

Errors return a consistent format:

```json
{
  "code": "TABLE_NOT_FOUND",
  "message": "Table 'users' does not exist",
  "hint": "Check the table name and ensure the schema has been created"
}
```

Common error codes:

| Code | Status | Description |
|------|--------|-------------|
| `TABLE_NOT_FOUND` | 404 | Table doesn't exist |
| `COLUMN_NOT_FOUND` | 404 | Column doesn't exist |
| `DATABASE_NOT_FOUND` | 404 | Database not found |
| `DATABASE_OUT_OF_SYNC` | 409 | Database version != template version |
| `MISSING_WHERE_CLAUSE` | 400 | UPDATE/DELETE requires where |
| `UNIQUE_VIOLATION` | 409 | Unique constraint failed |
| `FOREIGN_KEY_VIOLATION` | 409 | FK constraint failed |
| `QUERY_TOO_DEEP` | 400 | Exceeded max nesting depth |
| `BATCH_TOO_LARGE` | 400 | Exceeded max batch operations |
| `MISSING_DATABASE` | 400 | Database header required |

## License

Atomicbase is [fair-source](https://fair.io) licensed under [FSL-1.1-MIT](../LICENSE).
