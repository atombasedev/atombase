# @atomicbase/cli

Command-line interface for Atomicbase schema management.

## Installation

```bash
npm install -D @atomicbase/cli
# or
pnpm add -D @atomicbase/cli
```

## Configuration

Create `.env` or `atomicbase.config.ts` in your project root:

```bash
# .env
ATOMICBASE_URL=http://localhost:8080
ATOMICBASE_API_KEY=your-api-key
```

Or use a config file:

```typescript
// atomicbase.config.ts
import { defineConfig } from "@atomicbase/cli";

export default defineConfig({
  url: "http://localhost:8080",
  apiKey: "your-api-key",
  schemas: "./schemas",
});
```

## Commands

### Initialize Project

```bash
npx atomicbase init
```

Creates `atomicbase.config.ts` and `schemas/` directory.

### Templates

Manage schema templates on the server.

```bash
# List all templates
npx atomicbase templates list

# Get template details
npx atomicbase templates get <name>

# Push all local schemas to server
npx atomicbase templates push

# Push a specific template by name
npx atomicbase templates push <name>

# Pull all schemas from server to local files
npx atomicbase templates pull [-y]

# Pull a specific template
npx atomicbase templates pull <name> [-y]

# Preview changes without applying
npx atomicbase templates diff [file]

# Delete a template (only if no databases use it)
npx atomicbase templates delete <name> [-f]

# View version history
npx atomicbase templates history <name>

# Rollback to a previous version
npx atomicbase templates rollback <name> <version> [-f]
```

### Databases

Manage databases.

```bash
# List all databases
npx atomicbase databases list

# Get database details
npx atomicbase databases get <name>

# Create a new database
npx atomicbase databases create <name> --template <template>

# Delete a database
npx atomicbase databases delete <name> [-f]

# Sync database to latest template version
npx atomicbase databases sync <name>
```

### Jobs

Track migration jobs.

```bash
# List all jobs (optionally filter by status)
npx atomicbase jobs [--status <status>]

# Get job details
npx atomicbase jobs <job_id>

# Retry failed databases in a job
npx atomicbase jobs retry <job_id>
```

## Schema Files

Define schemas in the `schemas/` directory:

```typescript
// schemas/my-app.schema.ts
import { defineSchema, defineTable, c } from "@atomicbase/template";

export default defineSchema("my-app", {
  users: defineTable({
    id: c.integer().primaryKey(),
    name: c.text().notNull(),
    email: c.text().notNull().unique(),
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
  }),
});
```

## Workflow

1. Define schema locally in `schemas/`
2. Preview changes: `npx atomicbase templates diff`
3. Push to server: `npx atomicbase templates push`
4. Create databases: `npx atomicbase databases create acme --template my-app`
5. Requests to older databases migrate lazily on first access

## Options

```bash
# Skip SSL certificate verification (development only)
npx atomicbase -k templates list
npx atomicbase --insecure templates list
```

## License

Atomicbase is [fair-source](https://fair.io) licensed under [FSL-1.1-MIT](../../LICENSE).
