# @atomicbase/cli

Command-line interface for Atomicbase definition management.

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
  schemas: "./definitions",
});
```

## Commands

### Initialize Project

```bash
npx atomicbase init
```

Creates `atomicbase.config.ts` and `definitions/` directory.

### Definitions

Manage definitions on the server.

```bash
# List all definitions
npx atomicbase definitions list

# Get definition details
npx atomicbase definitions get <name>

# Push all local definition files to server
npx atomicbase definitions push

# Push a specific definition by name
npx atomicbase definitions push <name>

# Preview schema changes without applying
npx atomicbase definitions diff [file]

# View version history
npx atomicbase definitions history <name>

```

### Databases

Manage databases.

```bash
# List all databases
npx atomicbase databases list

# Get database details
npx atomicbase databases get <id>

# Create a new database
npx atomicbase databases create <id> --definition <definition>

# Delete a database
npx atomicbase databases delete <id> [-f]
```

## Definition Files

Define definitions in the `definitions/` directory:

- global definitions must be in `*.global.ts`
- user definitions must be in `*.user.ts`
- organization definitions must be in `*.org.ts`

```typescript
// definitions/my-app.global.ts
import { defineGlobal, defineSchema, defineAccess, defineTable, c, allow, isNull } from "@atomicbase/definitions";

const schema = defineSchema({
  users: defineTable({
    id: c.integer().primaryKey(),
    name: c.text().notNull(),
    email: c.text().notNull().unique(),
    created_at: c.text().notNull().default("CURRENT_TIMESTAMP"),
  }),
});

export default defineGlobal({
  schema,
  access: defineAccess(schema, {
    users: {
      select: allow(),
      insert: allow(),
      update: allow(),
      delete: allow(),
    },
  }),
});
```

Null checks use explicit helpers:

```typescript
users: {
  select: ({ prev }) => isNull(prev.deleted_at),
}
```

## Workflow

1. Define a definition locally in `definitions/`
2. Preview changes: `npx atomicbase definitions diff`
3. Push to server: `npx atomicbase definitions push`
4. Create databases: `npx atomicbase databases create acme --definition my-app`
5. Tenant databases migrate lazily on first access

## Options

```bash
# Skip SSL certificate verification (development only)
npx atomicbase -k definitions list
npx atomicbase --insecure definitions list
```

## License

Atomicbase is [fair-source](https://fair.io) licensed under [FSL-1.1-MIT](../../LICENSE).
