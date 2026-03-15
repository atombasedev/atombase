# Unified Tenant Model Architecture

> [!CAUTION]
> Historical design document. Some SDK examples and package names in this file predate the current definitions-first surface. Treat it as architecture background, not as the primary implementation guide.

## Summary

Replace the current Template/Database model with a unified Definition/Database model where `definition_type` determines ownership and access patterns.

## Core Concepts

### Two-Layer Hierarchy

```bash
Definition → Database
```

- **Definition**: Blueprint for schema + roles + access rules (stored with full version history)
- **Database**: Actual SQLite/Turso database, tracks owner and which definition version it's running

The `definition_type` determines how ownership and access work for databases created from that definition.

### Definition Types

| Type | Databases | Owner | Access Control |
|------|-----------|-------|----------------|
| `global` | Exactly 1 | None | Row-level access only |
| `organization` | Many | User (with members) | Roles + row-level access |
| `user` | Many | User (no members) | Row-level access only |

**Global** (`definition_type = 'global'`):

- Single database: one definition → one database (auto-created on push)
- Simpler access control: session context + row context (NO roles/RBAC)
- Database has `owner_id = NULL`

**Organization** (`definition_type = 'organization'`):

- Multi-database: one definition → many databases
- Full access control: session context + membership roles + row context
- Supports membership with roles via tenant-local `atombase_membership`
- `owner_id` is the billing/account owner (separate from RBAC roles)

**User** (`definition_type = 'user'`):

- Multi-database: one definition → many databases (one per user)
- Simpler access control: session context + row context (NO roles/RBAC)
- Owner is the user, no membership table used

### Ownership vs RBAC Roles (Organization)

For organization databases, there are two distinct concepts:

| Concept | Source | Purpose |
|---------|--------|---------|
| `owner_id` | `atombase_databases.owner_id` | Billing/account owner. Can transfer ownership. Cannot be removed. |
| `owner` role | `tenant.atombase_membership.role` | RBAC permission level. Multiple users can have this role. |

The `owner_id` user typically also has a membership row with `role = 'owner'`, but they're separate:

- `owner_id` answers: "Who is responsible for this database?"
- `role = 'owner'` answers: "What can this user do?"

This separation allows:
- Transferring billing ownership without changing RBAC
- Multiple users with owner-level permissions
- A fallback if tenant membership rows are accidentally deleted

### Shared Infrastructure
- Schema engine identical for all types (tables, columns, indexes, FTS)
- Version history tracked in `atombase_definitions_history`
- Access rules stored alongside schema in history

### Migration System
- **Migrations**: `atombase_migrations` stores migration SQL (from_version → to_version)
- **Lazy migrations**: Databases track their `definition_version`, migrate on access
- **Failure tracking**: `atombase_migration_failures` for debugging

### Roles (Organization)

Roles are defined as a simple array. Only applicable to `organization` type:

```typescript
roles: ["owner", "billing", "admin", "member", "viewer"]
```

Roles have no built-in hierarchy — permissions are explicitly defined in `management` and `access`.

### Management Permissions (Organization)

The `management` block defines who can manage organization membership and perform org-level operations. Use `defineMembership({ roles, management })` to get type-safe role references:

```typescript
export default defineOrg({
  membership: defineMembership({
    roles: ["owner", "admin", "member", "viewer"],
    management: (role) => ({
      owner: {
        invite: role.any(),
        assignRole: role.any(),
        removeMember: role.any(),
        updateOrg: true,
        deleteOrg: true,
        transferOwnership: true,
      },
      admin: {
        invite: [role.member, role.viewer],
        assignRole: [role.member, role.viewer],
        removeMember: [role.member, role.viewer],
      },
    }),
  }),
  // ...
});
```

**Structure:**
- Keys are role names (must match `roles` array)
- `invite`, `assignRole`, `removeMember` — which target roles this role can manage
  - `role.any()` — can manage all roles
  - `[role.member, role.viewer]` — can only manage specific roles
- `updateOrg`, `deleteOrg`, `transferOwnership` — binary permissions (`true` to allow)

**Notes:**
- All members can view the member list (no permission needed)
- Invitations require acceptance — users cannot be added directly
- Roles not listed have no management permissions
- The `owner_id` (billing owner) can always access membership management as a fallback
- If `management` is omitted entirely, defaults to: only `owner` can manage

**Default management permissions (if not specified):**
```typescript
membership: defineMembership({
  roles: ["owner"],
  management: (role) => ({
    owner: {
      invite: role.any(),
      assignRole: role.any(),
      removeMember: role.any(),
      updateOrg: true,
      deleteOrg: true,
      transferOwnership: true,
    },
  }),
})
```

### Access Policy Context

Access policies use `r.where(({ auth, old, new }) => ...)` to define row-level security. The available context depends on the operation:

| Operation | `auth` | `old` | `new` |
|-----------|--------|-------|-------|
| SELECT | ✓ | ✓ | — |
| INSERT | ✓ | — | ✓ |
| UPDATE | ✓ | ✓ | ✓ |
| DELETE | ✓ | ✓ | — |

- **`auth`** — the authenticated user (`auth.id`, and `auth.role` for org databases)
- **`old`** — the existing row being acted upon (not available for INSERT since no row exists yet)
- **`new`** — the resulting row after modification (not available for SELECT/DELETE since no modification persists)

**Examples:**
```typescript
access: defineAccess({
  posts: definePolicy({
    // Anyone can read
    select: r.allow(),
    // Can only insert posts where you're the author
    insert: r.where(({ auth, new }) => eq(new.author_id, auth.id)),
    // Can only update your own posts
    update: r.where(({ auth, old }) => eq(old.author_id, auth.id)),
    // Can only delete your own posts
    delete: r.where(({ auth, old }) => eq(old.author_id, auth.id)),
  }),
}),
```

For UPDATE, you can check both `old` and `new` to enforce constraints on what changes are allowed:

```typescript
update: r.where(({ auth, old, new }) =>
  eq(old.author_id, auth.id) && eq(new.author_id, old.author_id)
),  // Can update own posts, but can't change the author
```

## SDK Patterns

### Client Setup

**Browser client** — stores session in memory:

```typescript
import { createClient } from "@atombase/sdk";

const client = createClient({
  url: "https://api.atombase.com",
});
```

**SSR client** — uses cookies for session persistence across requests:

```typescript
import { createClient } from "@atombase/sdk";
import { cookies } from "next/headers";  // or your framework's cookie API

const client = createClient({
  url: process.env.ATOMBASE_URL,
  cookies: {
    get: (name) => cookies().get(name)?.value,
    set: (name, value, options) => cookies().set(name, value, options),
    delete: (name) => cookies().delete(name),
  }
});
```

**Cookie handler interface:**

```typescript
interface CookieHandler {
  get(name: string): string | undefined;
  set(name: string, value: string, options?: CookieOptions): void;
  delete(name: string): void;
}

interface CookieOptions {
  httpOnly?: boolean;
  secure?: boolean;
  sameSite?: "strict" | "lax" | "none";
  maxAge?: number;  // seconds
  path?: string;
}
```

When `cookies` is provided, the SDK automatically:
- Sets cookie on `signIn()` (on your app's domain, not atombase's)
- Reads cookie for all authenticated requests
- Deletes cookie on `signOut()`

### Data Access Patterns

The SDK distinguishes between **data fetching** and **operation handles**:

- `get*()` methods return plain data objects (no methods, one request)
- `client.*()` methods return operation handles (each operation is one request)

This avoids unnecessary roundtrips while keeping the API intuitive.

### Reading Data

```typescript
// Get current user data — returns plain object
const userData = await client.auth.getUser()
// { id: "user-123", email: "alice@example.com", ... }

// Get org data for an org the user is a member of — returns plain object
const orgData = await client.user.getOrg("acme-corp")
// { id: "acme-corp", name: "Acme Corp", role: "admin", ... }

// List all orgs user is a member of
const orgs = await client.user.getOrgs()
// [{ id: "acme-corp", name: "Acme Corp", role: "admin" }, ...]
```

### Database Operations

Each operation is a single roundtrip. The server validates session + authorization inline.

```typescript
// User database — user implicit from session
await client.user.from("entries").select()

// Organization database — specify which org
await client.org("acme-corp").from("people").select()

// Global database — no ownership, just the definition name
await client.global("marketplace").from("extensions").select()
```

**Headers sent:**
- User: `Database: user:notes` (definition name from user's database)
- Org: `Database: org:acme-corp`
- Global: `Database: global:marketplace`

### Membership Management

```typescript
// Invite user to org (requires acceptance)
await client.org("acme-corp").invites.send(email, "member")

// List pending invitations
const invites = await client.org("acme-corp").invites.list()

// Revoke invitation (anyone who can invite can revoke)
await client.org("acme-corp").invites.revoke(inviteId)

// List members
const members = await client.org("acme-corp").members.list()

// Change member role
await client.org("acme-corp").members.setRole(userId, "admin")

// Remove member
await client.org("acme-corp").members.remove(userId)
```

### Auth Operations

```typescript
// Sign in — returns session
const session = await client.auth.signIn({ email, password })

// Sign out
await client.auth.signOut()

// Get current session
const session = await client.auth.getSession()

// Create org — creator becomes owner
const org = await client.user.createOrg("acme-corp", { definition: "customer" })

// Create user with personal database
const user = await client.auth.createUser({ email, password }, { database: "notes" })

// Create user without personal database
const user = await client.auth.createUser({ email, password })
```

### Why This Pattern?

The hierarchical pattern `user → org → database` would require multiple roundtrips:

```typescript
// This would be 3 requests (bad)
const user = await client.auth.getUser()
const org = await user.getOrg("acme-corp")
await org.from("people").select()
```

Instead, `client.org("acme-corp")` returns a lightweight handle, not a fetched object. The server validates everything in one request:

```
POST /data/query/people
Database: org:acme-corp
Authorization: Bearer <session>
```

When you need the actual org data (name, metadata, etc.), use `getOrg()`. When you just need to operate on it, use the handle.

## File Naming Convention

```
definitions/
  +customer.org.ts       # Organization database
  +notes.user.ts         # User database
  +marketplace.global.ts # Global database
  shared-columns.ts      # Helper file, importable but not processed
  access-helpers.ts      # Helper file, importable but not processed
```

- `+*.org.ts` - Organization databases (CLI processes)
- `+*.user.ts` - User databases (CLI processes)
- `+*.global.ts` - Global databases (CLI processes)
- `*.ts` - Helper files (can be imported by definitions, CLI ignores)

**Name resolution:** Derived from filename (`+customer.org.ts` → `"customer"`).

**CLI validation:**
- `+*.org.ts` must `export default defineOrg(...)` — error otherwise
- `+*.user.ts` must `export default defineUser(...)` — error otherwise
- `+*.global.ts` must `export default defineGlobal(...)` — error otherwise

**Push/Pull behavior:**
- `push` evaluates the TypeScript and sends the flattened schema to the API
- `pull` writes the flattened schema back to definition files
- Helper files are local-only convenience — pull will overwrite `+*` files with flattened versions
- Refactor into helpers after pulling if desired

## File Formats

**Organization database** (`definitions/+customer.org.ts`):
```typescript
import { defineOrg, defineMembership, defineSchema, defineAccess, defineTable, definePolicy, c, r, eq, inList } from "@atomicbase/definitions";

export default defineOrg({
  maxMembers: 50,
  membership: defineMembership({
    roles: ["owner", "admin", "member", "viewer"],
    management: (role) => ({
      owner: {
        invite: role.any(),
        assignRole: role.any(),
        removeMember: role.any(),
        updateOrg: true,
        deleteOrg: true,
        transferOwnership: true,
      },
      admin: {
        invite: [role.member, role.viewer],
        assignRole: [role.member, role.viewer],
        removeMember: [role.member, role.viewer],
      },
    }),
  }),
  schema: defineSchema({
    projects: defineTable({
      id: c.integer().primaryKey(),
      name: c.text().notNull(),
      created_by: c.text().notNull(),
    }),
  }),
  access: defineAccess({
    projects: definePolicy({
      select: r.allow(),
      insert: r.where(({ auth }) => inList(auth.role, ["member", "admin", "owner"])),
      delete: r.where(({ auth }) => inList(auth.role, ["owner", "admin"])),
    }),
  }),
});
```

**User definition** (`definitions/+notes.user.ts`):
```typescript
import { defineUser, defineSchema, defineAccess, defineTable, definePolicy, c, r } from "@atomicbase/definitions";

export default defineUser({
  schema: defineSchema({
    notes: defineTable({
      id: c.integer().primaryKey(),
      content: c.text().notNull(),
      created_at: c.text().notNull(),
    }),
  }),
  access: defineAccess({
    notes: definePolicy({
      select: r.allow(),
      insert: r.allow(),
      update: r.allow(),
      delete: r.allow(),
    }),
  }),
});
```

**Global definition** (`definitions/+marketplace.global.ts`):
```typescript
import { defineGlobal, defineSchema, defineAccess, defineTable, definePolicy, c, r, eq } from "@atomicbase/definitions";

export default defineGlobal({
  schema: defineSchema({
    extensions: defineTable({
      id: c.integer().primaryKey(),
      author_id: c.text().notNull(),
      name: c.text().notNull(),
    }),
  }),
  access: defineAccess({
    extensions: definePolicy({
      select: r.allow(),
      insert: r.where(({ auth, new }) => eq(new.author_id, auth.id)),
      update: r.where(({ auth, old }) => eq(old.author_id, auth.id)),
      delete: r.where(({ auth, old }) => eq(old.author_id, auth.id)),
    }),
  }),
});
```

## Platform Tables

See `api/unified_tenant_schema.sql` for the full schema. Key tables:

**Identity & Auth:**
- `atombase_definitions` — schema blueprints with `definition_type` (global/organization/user)
- `atombase_databases` — pure storage, linked to definitions
- `atombase_users` — user accounts with optional `database_id`
- `atombase_organizations` — identity layer on databases
- tenant-local invitation tables in the organization database — pending org invites
- `atombase_sessions` — session tokens

**Tenant-local auth state (organization databases):**
- `atombase_membership` — org membership with role (stored in each org tenant database)

**Schema & Versioning:**
- `atombase_definitions_history` — schema snapshots per version
- `atombase_migrations` — migration SQL between versions
- `atombase_migration_failures` — debugging failed migrations

**Policies (normalized for efficient caching):**
```sql
-- Access policies: one row per table/operation
-- NULL or '' conditions_json = allow, non-empty = RLS conditions
CREATE TABLE atombase_access_policies (
    definition_id INTEGER NOT NULL REFERENCES atombase_definitions(id),
    version INTEGER NOT NULL,
    table_name TEXT NOT NULL,
    operation TEXT NOT NULL CHECK(operation IN ('select', 'insert', 'update', 'delete')),
    conditions_json TEXT,
    PRIMARY KEY(definition_id, version, table_name, operation)
);

-- Management policies: one row per role/action
-- NULL or '' target_roles_json = allowed for any target, non-empty = specific target roles
CREATE TABLE atombase_management_policies (
    definition_id INTEGER NOT NULL REFERENCES atombase_definitions(id),
    role TEXT NOT NULL,
    action TEXT NOT NULL CHECK(action IN ('invite', 'assignRole', 'removeMember', 'updateOrg', 'deleteOrg', 'transferOwnership')),
    target_roles_json TEXT,
    PRIMARY KEY(definition_id, role, action)
);
```

**Policy logic:**
1. No row exists → deny (operation/action not configured)
2. Row exists, conditions empty → allow all
3. Row exists, conditions present → apply RLS/check target roles

## Policy Compilation

Access policy conditions compile entirely to runtime enforcement. No triggers or CHECK constraints are generated from access policies.

### Condition Format

Conditions support AND/OR/NOT nesting:

```json
// Simple: author can edit their own posts
{"field": "old.author_id", "op": "eq", "value": "auth.id"}

// OR: author can edit OR admin can edit anything
{
  "or": [
    {"field": "old.author_id", "op": "eq", "value": "auth.id"},
    {"field": "auth.role", "op": "eq", "value": "admin"}
  ]
}

// NOT: can delete if not published
{"not": {"field": "old.status", "op": "eq", "value": "published"}}

// Nested AND/OR/NOT supported
```

**Operators:** `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `in`, `is` (NULL), `is_not` (NOT NULL)

### Evaluation Rules

Conditions are evaluated with three-valued logic:
- **pass** — `new.*`/`auth.*` condition satisfied in Go
- **fail** — condition cannot be satisfied
- **maybe** — has `old.*` conditions, defer to SQL

**For OR:** If any `new.*`/`auth.*` branch passes → OR satisfied, no WHERE needed. Otherwise, surviving `old.*` branches become OR'd WHERE clause.

**For AND:** If any branch fails → AND fails. Otherwise, combine `old.*` branches into AND'd WHERE clause.

**Example:**
```json
{"or": [
  {"field": "old.author_id", "op": "eq", "value": "auth.id"},
  {"field": "new.status", "op": "eq", "value": "draft"}
]}
```

Request `{status: "draft"}` → `new.*` passes → no WHERE needed
Request `{status: "published"}` → `new.*` fails → WHERE: `author_id = ?`

### Compile-Time Validation

Reject at definition push if:
- `old.*` condition in INSERT policy (no existing row)
- `new.*` condition in SELECT/DELETE policy (no new values)
- `auth.role` condition in global/user definition (no roles)

### Batch Semantics

Batch inserts are atomic — if any row fails validation, reject the entire batch. Matches Postgres RLS behavior.

### System Bypass

System operations (background jobs, migrations, admin) skip policy enforcement entirely.

### Data Payloads

- INSERT/UPDATE values are always literals (parameterized)
- No SQL expressions in data payloads
- Use schema defaults or client-side values for computed fields

See `implementing-tenant-model.md` for full implementation details.

## Session Validation

Sessions use a two-step validation with optimized write frequency:

1. **Validate session** — lookup by ID, verify secret hash with constant-time comparison
2. **Check expiry** — hard expiry (`expires_at`) and soft expiry (inactivity via `last_verified_at`)
3. **Update activity** — only write `last_verified_at` every ~1 hour, not per request

```go
const (
    inactivityTimeout     = 10 * 24 * time.Hour  // 10 days
    activityCheckInterval = 1 * time.Hour        // 1 hour
)
```

See `implementing-tenant-model.md` for full implementation.

## Database Resolution Queries

After session validation, resolve target database access based on `Database` header. Primary DB resolution only determines routing (`database_id`, `definition_id`, `definition_version`) and user identity (`auth.id`). Organization role resolution happens in the tenant database during query planning/execution.

**Global** (`Database: global:<definition_name>`)
```sql
SELECT d.id, d.definition_id, d.definition_version
FROM atombase_definitions def
JOIN atombase_databases d ON d.definition_id = def.id
WHERE def.name = ? AND def.definition_type = 'global'
```

**User** (`Database: user:<definition_name>`)
```sql
SELECT d.id, d.definition_id, d.definition_version
FROM atombase_users u
JOIN atombase_databases d ON d.id = u.database_id
JOIN atombase_definitions def ON def.id = d.definition_id
WHERE u.id = ? AND def.name = ? AND def.definition_type = 'user'
```

**Org** (`Database: org:<organization_id>`)
```sql
SELECT d.id, d.definition_id, d.definition_version
FROM atombase_organizations o
JOIN atombase_databases d ON d.id = o.database_id
WHERE o.id = ?
```

**Tenant-side role lookup (organization databases)**
```sql
SELECT role
FROM atombase_membership
WHERE user_id = ?
```

The runtime planner combines:
1. `auth.id` from session validation (primary)
2. Cached access policy from primary (`definition_id`, `version`)
3. Tenant-local org role from `atombase_membership`

## Policy Caching

**Cache keys:**
- Access: `access:{definition_id}:{version}:{table}:{operation}`
- Management: `mgmt:{definition_id}:{role}:{action}`

**Cache lifecycle:**
- **Startup** — Rebuild entire cache from `atombase_access_policies`
- **Definition push** — Invalidate all cache entries for that definition
- **Version in key** — New versions naturally use new cache entries

**Batch loading for joins:**

When querying with joins, batch load all table policies in one query:

```sql
SELECT table_name, conditions_json
FROM atombase_access_policies
WHERE definition_id = ? AND version = ? AND operation = ?
AND table_name IN ('posts', 'comments', 'users')
```

Each table gets its own RLS conditions injected. If any table has no policy row, the query fails.

## Role Validation

Roles are validated at write time, not query time:

- **Membership insert/update** — Check role exists in definition `roles_json` before writing tenant membership
- **Invalid role** — Reject with 400 error before writing to database
- **Query time** — No validation needed (data already clean)

## API Endpoints

### Definitions
- `POST /platform/definitions` - Create definition (global, organization, or user)
- `GET /platform/definitions/{name}` - Get definition
- `POST /platform/definitions/{name}/push` - Push new schema version
- `GET /platform/definitions/{name}/history` - Get version history

### Databases
- `GET /platform/databases/{id}` - Get database
- `POST /platform/databases/{id}/migrate` - Trigger migration to latest version

### Organizations
- `POST /platform/organizations` - Create organization
- `GET /platform/organizations/{id}` - Get organization
- `PATCH /platform/organizations/{id}` - Update organization (name, metadata)
- `DELETE /platform/organizations/{id}` - Delete organization

### Membership (organizations only)
- `GET /platform/organizations/{id}/members` - List members
- `PATCH /platform/organizations/{id}/members/{user_id}` - Update role
- `DELETE /platform/organizations/{id}/members/{user_id}` - Remove member

### Invitations (organizations only)
- `POST /platform/organizations/{id}/invitations` - Send invitation
- `GET /platform/organizations/{id}/invitations` - List pending invitations
- `DELETE /platform/organizations/{id}/invitations/{invitation_id}` - Revoke invitation

### Users
- `POST /platform/users` - Create user
- `GET /platform/users/{id}` - Get user
- `PATCH /platform/users/{id}` - Update user

### Sessions
- `POST /platform/sessions` - Create session (login)
- `DELETE /platform/sessions/{id}` - Delete session (logout)

## Request Headers

```
Authorization: Bearer <session_token>   # User session
Database: <type>:<name>                 # Target database
```

**Database header format:**
- `global:<definition_name>` — Global database (e.g., `global:marketplace`)
- `user:<definition_name>` — User's personal database (e.g., `user:notes`)
- `org:<organization_id>` — Organization database (e.g., `org:acme-corp`)

## Implementation Phases

### Phase 1: TypeScript packages
1. Create `@atomicbase/definitions` package with condition primitives
2. Add `defineOrg()`, `defineUser()`, and `defineGlobal()` to `@atomicbase/definitions`
3. Update CLI parser for `.org.ts`, `.user.ts`, and `.global.ts` file detection

### Phase 2: Go API - Platform Tables
1. Add unified platform tables to `api/schema.sql`
2. Add types to `api/platform/types.go`
3. Create `api/platform/definitions.go` - unified definition CRUD
4. Create `api/platform/users.go` - user management
5. Create `api/platform/sessions.go` - session management

### Phase 3: Go API - Database Management
1. Create `api/platform/databases.go` - database CRUD
2. Create `api/platform/membership.go` - manage org membership rows in tenant databases

### Phase 4: Schema Versioning
1. Implement `atombase_definitions_history` population on push
2. Implement `atombase_migrations` for migration SQL generation
3. Implement lazy migration on database access (check `definition_version`)
4. Track failures in `atombase_migration_failures`

### Phase 5: Auth Context
1. Update `api/tools/auth.go` with session-based auth context
2. Resolve org role from tenant `atombase_membership` using `auth.id`
3. Inject access conditions into queries from cached primary policies + tenant role context

### Phase 6: Migration path
1. CLI commands to migrate existing templates to definitions
2. Deprecation warnings for `.schema.ts` files
3. Keep existing `/platform/templates/*` endpoints working temporarily

## Critical Files to Modify

**Go API:**
- `api/schema.sql` - Add unified platform tables
- `api/platform/types.go` - Add Definition, Database, User, Session types
- `api/platform/handlers.go` - Register new routes
- `api/tools/auth.go` - Session-based auth context middleware

**TypeScript:**
- `packages/definitions/src/index.ts` - New unified package (defineOrg, defineUser, defineGlobal, defineManagement, defineSchema, defineTable, defineAccess, definePolicy)
- `packages/cli/src/schema/parser.ts` - File type detection (.org.ts, .user.ts, .global.ts)
- `packages/cli/src/commands/templates.ts` - Route to correct API based on type

**New files:**
- `packages/definitions/` - New package replacing template + access
- `api/platform/definitions.go` - Unified definition CRUD
- `api/platform/databases.go` - Database management
- `api/platform/membership.go` - Tenant-local organization membership management
- `api/platform/users.go` - User management
- `api/platform/sessions.go` - Session management

## Verification

### Organization Flow
1. Create a `.org.ts` file with schema, roles, management, and access
2. Push via CLI - verify definition created with version in `atombase_definitions`
3. Verify `atombase_definitions_history` has schema + access JSON
4. Create database via API - verify entry in `atombase_databases` with `owner_id` set
5. Add user membership with role in the organization tenant `atombase_membership`
6. Make data requests with different roles - verify RBAC enforcement
7. Update schema, push again - verify new version in history
8. Access database - verify lazy migration and `definition_version` update

### User Flow
1. Create a `.user.ts` file with schema and access
2. Push via CLI - verify definition with `definition_type = 'user'`
3. Create database - verify `owner_id` set, no membership rows
4. Make data requests as owner - verify RLS enforcement
5. Update schema - verify migration works

### Global Flow
1. Create a `.global.ts` file with schema and access
2. Push via CLI - verify definition with `definition_type = 'global'`
3. Verify single database auto-created with `owner_id = NULL`
4. Make data requests - verify RLS enforcement (no roles)
5. Update schema - verify migration works

### Migration Failures
1. Introduce a breaking schema change
2. Trigger lazy migration
3. Verify failure logged in `atombase_migration_failures`
