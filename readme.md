# Atomicbase

**Manage a million databases like it's one.**

Atomicbase is the Turso development platform, packaged as a single Go executable.

## Philosophy

At its core, Atomicbase was built to make multi-database systems more predictable and reliable. There are a lot of moving parts in a multi-database system. And in situations where this architecture is used, high security and reliability are the top priorities. One small mistake can cause a database to go out of sync, corrupt, or be vulnerable to attacks. Every design choice was made with security and reliability first. We're not just building a reliable platform. We're creating a platform that makes building reliable applications on top of it feel easy.

> [!WARNING]
> **Atomicbase is in experimental preview.** There are many known and unknown bugs. APIs are likely to change.

## Status

| Component        | Status       |
| ---------------- | ------------ |
| Data API         | Alpha        |
| TypeScript SDK   | Alpha        |
| Platform API     | Experimental |
| Schema Templates | Experimental |
| Template Package | Alpha        |
| CLI              | Alpha        |
| Authentication   | In progress  |
| AI               | In progress  |
| File Storage     | Planned      |
| Realtime         | Planned      |
| Dashboard        | Planned      |

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│                                          Atomicbase                                         │
├─────────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                             │
│     ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐     │
│     │    Auth     ├──┤    Data     ├──┤  Platform   ├──┤     AI      ├──┤   Storage   │     │
│     │             ├──┤             ├──┤             ├──┤             ├──┤             │     │
│     │ • Users     ├──┤ • Queries   ├──┤ • Databases ├──┤ • Models    ├──┤ • Uploads   │     │
│     │ • Orgs      ├──┤ • Validation├──┤ • Templates ├──┤ • Context   ├──┤ • Transforms│     │
│     │ • Roles     ├──┤ • DBs       ├──┤ • Migrations├──┤ • KB        ├──┤ • CDN       │     │
│     │ • SSO       ├──┤   routing   ├──┤ • Syncing   ├──┤ • Isolation ├──┤ • Caching   │     │
│     └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘     │
│            └────────────────┴────────────────┼────────────────┴────────────────┘            │
│    ┌─────────────────────────────────────────┴─────────────────────────────────────────┐    │
│    │                                      Databases                                    │    │
│    │    ┌─────────┐  ┌──────────┐  ┌─────────┐  ┌─────────┐  ┌────────┐  ┌───────┐     │    │x`
│    │    │ Acme Co │  │ Plex Inc │  │ Grav LLC│  │ Northrn │  │ Adatum │  │  ...  │     │    │
│    │    └─────────┘  └──────────┘  └─────────┘  └─────────┘  └────────┘  └───────┘     │    │
│    └───────────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                             │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

## Quick Start

```bash
cd api
```

### 1. Set Environment Variables

```ini
TURSO_API_KEY="your-turso-key"
TURSO_ORGANIZATION="your-turso-org"

ATOMICBASE_CORS_ORIGINS="http://localhost:3000,http://localhost:5173"
ATOMICBASE_API_KEY="your-api-key"
```

### 2. Start API Server

```bash
make run
```

Server runs at `http://localhost:8080` by default.

### 3. Install Packages

```bash
npm install @atomicbase/sdk @atomicbase/template
```

### 4. Initialize Config

```bash
npx atomicbase init
```

Creates `atomicbase.config.ts` file & schemas folder

### 5. Define & Push Schema

```typescript
// schemas/my-app.schema.ts
import { defineSchema, defineTable, c } from "@atomicbase/template";

export default defineSchema("my-app", {
  users: defineTable({
    id: c.integer().primaryKey(),
    name: c.text().notNull(),
    email: c.text().notNull().unique(),
  }),
});
```

```bash
npx atomicbase templates push
```

### 6. Create a Database

```typescript
import { createClient } from "@atomicbase/sdk";

const client = createClient({
  url: "http://localhost:8080",
  apiKey: "your-api-key",
});

await client.databases.create({ name: "acme-corp", template: "my-app" });
```

### 7. Query Data

```typescript
import { eq } from "@atomicbase/sdk";

const acme = client.database("acme-corp");

// Insert
await acme.from("users").insert({ name: "Alice", email: "alice@example.com" });

// Select
const { data } = await acme.from("users").select();

// Update
await acme.from("users").update({ name: "Alicia" }).where(eq("id", 1));

// Delete
await acme.from("users").delete().where(eq("id", 1));
```

## Important Decisions

### 1. Reliability Over Control

Tight rules like TypeScript-only schemas and no direct SQL access trade off control for reliability. A database-per-customer system has a lot of moving parts. These multi-database systems are often used in B2B situations where high security and reliability are crucial. One small mistake can cause a database to go out of sync or make it vulnerable to attacks directly through the API. That's exactly why we avoid custom SQL, define schemas idempotently per template, and put querying in a completely separate API from schema changes.

### 2. Fair-Source License

Our fair-source license allows anyone to self-host Atomicbase in production for free but provides us protection against competing hosted services. We want self-hosting Atomicbase to feel simple and reliable. Atomicbase is a single binary and comes with a docker and fly config that makes self-hosting trivial. But that same simplicity makes it easy for third parties to commoditize our software. Fair-source gives us a sustainable business model and you the best possible self-hostable software.

### 3. Single Binary

Atomicbase was built on top of Go and SQLite for an important reason. They're both incredibly simple and powerful. They make it easy to set up your architecture, maintain it, and scale it. We feel strongly that Atomicbase must live up to this same standard. Packing everything into a single binary and paying attention to all the tiny details that make a software feel simple to use is how we live up to this standard.

### 4. TypeScript Schema Templates

We decided to make our schema templates idempotent TypeScript files so that schemas felt easy to manage and ready for the future of development with AI. Anyone can set up their entire platform's schema through TypeScript and our CLI.

### 5. Strict Database Versions

We decided that requiring each database to be on the latest version of its template to be accessed was the best way to prevent weird/silent errors. If you change the expected shape of your databases, having a database left out of sync could create weird, potentially dangerous behaviour. Even though we strive to make our migration system as robust as it can be, it's always possible to have a database out of sync when you're managing so many.

### 6. Session-based Authentication

This is another design choice centered around simplicity and security. JWTs are not inherently insecure, but making them fully secure is overly complicated for both us and anyone using Atomicbase. Sessions are uniquely powerful for an SQLite system as well because database reads are incredibly fast and inexpensive. Lucia has a great discussion about why they switched from JWTs to sessions: [Lucia discussion](https://github.com/lucia-auth/lucia/discussions/112)

### 7. POST-only query API

All query operations go through one route: `POST /data/query`. While it feels wrong to do a select or a delete through POST, we tested full REST-style and it was just too cumbersome. Adding where conditions, joins, ordering, etc to GET and DELETE methods that don't support request bodies has too many edge cases.

## Examples

- [react-todo](./examples/react-todo) - Next.js todo app with Google OAuth and database-per-user architecture

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

## License

Atomicbase is [fair-source](https://fair.io) licensed under [FSL-1.1-MIT](./LICENSE). You can use, modify, and self-host the software freely for your own applications. The only restriction is offering Atomicbase as a competing hosted service. The license converts to MIT after two years.
