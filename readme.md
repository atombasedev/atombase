# AtomBase

**Launch your SaaS without rebuilding the backend.**

AtomBase is the SaaS-native backend for a database-per-tenant architecture, built on distributed SQLite.

> [!CAUTION]
> **Prototype software - not complete.**
> AtomBase is still under active development and is not production-ready. Expect missing features, API changes, and rough edges.

## What is AtomBase?

AtomBase helps you run one database per tenant while still feeling like you are working with a single backend.

- **Databases Everywhere**: spin up a database per tenant in seconds.
- **Definitions**: define schemas and access patterns in code.
- **Data APIs**: query tenant databases securely over HTTP.
- **Managed Migrations**: keep tenant schemas in sync through definition versions.
- **Authentication**: built-in magic-link auth and session-backed browser access.
- **Storage**: coming soon.
- **AI**: coming soon.

## Prototype Status

| Component        | Status       |
| ---------------- | ------------ |
| Data API         | Alpha        |
| TypeScript SDK   | Alpha        |
| Platform API     | Experimental |
| Definitions      | Experimental |
| CLI              | Alpha        |
| Authentication   | Alpha        |
| AI               | In progress  |
| File Storage     | Planned      |
| Realtime         | Planned      |
| Dashboard        | Planned      |

This repository currently represents a working prototype, not a finished product.

## Quick Start

```bash
cd api
```

### 1) Set environment variables

```ini
TURSO_API_KEY="your-turso-key"
TURSO_ORGANIZATION="your-turso-org"

ATOMICBASE_CORS_ORIGINS="http://localhost:3000,http://localhost:5173"
ATOMICBASE_API_KEY="your-api-key"
```

### 2) Start the API

```bash
make run
```

By default the server runs at `http://localhost:8080`.

### 3) Install the SDK and definitions package

```bash
npm install @atomicbase/sdk @atomicbase/definitions
```

### 4) Initialize project config

```bash
npx atomicbase init
```

### 5) Define and push a definition

```typescript
import { defineGlobal, defineSchema, defineAccess, defineTable, c, allow } from "@atomicbase/definitions";

const schema = defineSchema({
  users: defineTable({
    id: c.integer().primaryKey(),
    name: c.text().notNull(),
    email: c.text().notNull().unique(),
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

```bash
npx atomicbase definitions push
```

### 6) Create a tenant database

```typescript
import { createClient } from "@atomicbase/sdk";

const client = createClient({
  url: "http://localhost:8080",
  apiKey: "your-api-key",
});

await client.databases.create({ id: "acme-corp", definition: "my-app" });
```

### 7) Query tenant data

```typescript
import { eq } from "@atomicbase/sdk";

const acme = client.database("global:acme-corp");

await acme.from("users").insert({ name: "Alice", email: "alice@example.com" });
const { data } = await acme.from("users").select();
await acme.from("users").update({ name: "Alicia" }).where(eq("id", 1));
await acme.from("users").delete().where(eq("id", 1));
```

### 8) Browser auth and user databases

For browser apps, the intended path is:

1. start magic-link auth
2. complete login and store the returned session token client-side
3. restore that token on app boot
4. create a session-backed SDK client
5. call `client.auth.me()`
6. if `databaseId` is missing, self-provision with `client.auth.createDatabase({ definition })`
7. use `client.database()` with no `Database` header to access the current user's database directly from the browser

```typescript
import { createClient } from "@atomicbase/sdk";

const baseClient = createClient({ url: "http://localhost:8080" });

const completed = await baseClient.auth.completeMagicLink(tokenFromEmail);
if (completed.error) throw completed.error;

localStorage.setItem("atombase.session", completed.data.token);

const client = baseClient.withSession(completed.data.token);
const me = await client.auth.me();
if (me.error) throw me.error;

if (!me.data.databaseId) {
  const provisioned = await client.auth.createDatabase({ definition: "todo-app" });
  if (provisioned.error) throw provisioned.error;
}

const todoDb = client.database();
```

## Key Ideas

- **Tenant isolation by default**: each tenant gets its own database.
- **Definitions keep systems aligned**: define once, roll forward with migrations.
- **Strict versions + lazy sync**: out-of-date tenant databases are synchronized when accessed.
- **Simple operational model**: single Go service with a focused API surface.

## Roadmap (incomplete)

- Enterprise authentication (orgs, RBAC, SSO, RLS)
- Storage APIs and object workflows
- AI APIs with tenant-scoped context
- Dashboard and improved operator tooling

## Examples

- [react-todo](./examples/react-todo) - legacy Next.js todo example using Google OAuth and the old template-era model

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

AtomBase is [fair-source](https://fair.io) licensed under [FSL-1.1-MIT](./LICENSE). You can use, modify, and self-host the software freely for your own applications. The only restriction is offering AtomBase as a competing hosted service. The license converts to MIT after two years.
