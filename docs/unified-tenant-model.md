# Unified Tenant Model Architecture

## Summary

Replace the current Template/Database model with a unified Definition/Database model where `definition_type` determines ownership and access patterns.

## Core Concepts

### Two-Layer Hierarchy

```
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
- Supports membership with roles via `atomicbase_membership`
- `owner_id` is the billing/account owner (separate from RBAC roles)

**User** (`definition_type = 'user'`):
- Multi-database: one definition → many databases (one per user)
- Simpler access control: session context + row context (NO roles/RBAC)
- Owner is the user, no membership table used

### Ownership vs RBAC Roles (Organization)

For organization databases, there are two distinct concepts:

| Concept | Source | Purpose |
|---------|--------|---------|
| `owner_id` | `atomicbase_databases.owner_id` | Billing/account owner. Can transfer ownership. Cannot be removed. |
| `owner` role | `atomicbase_membership.role` | RBAC permission level. Multiple users can have this role. |

The `owner_id` user typically also has a membership row with `role = 'owner'`, but they're separate:
- `owner_id` answers: "Who is responsible for this database?"
- `role = 'owner'` answers: "What can this user do?"

This separation allows:
- Transferring billing ownership without changing RBAC
- Multiple users with owner-level permissions
- A fallback if all membership rows are accidentally deleted

### Shared Infrastructure
- Schema engine identical for all types (tables, columns, indexes, FTS)
- Version history tracked in `atomicbase_definitions_history`
- Access rules stored alongside schema in history

### Migration System
- **Migrations**: `atomicbase_migrations` stores migration SQL (from_version → to_version)
- **Lazy migrations**: Databases track their `definition_version`, migrate on access
- **Failure tracking**: `atomicbase_migration_failures` for debugging

### Roles (Organization)

Roles are defined as a simple array. Only applicable to `organization` type:

```typescript
roles: ["owner", "billing", "admin", "member", "viewer"]
```

Roles have no built-in hierarchy — permissions are explicitly defined in `management` and `access`.

### Management Permissions (Organization)

The `management` block defines who can manage organization membership and perform org-level operations. Use `defineManagement((role) => ({...}))` to get type-safe role references:

```typescript
export default defineOrg({
  roles: ["owner", "admin", "member", "viewer"],
  management: defineManagement((role) => ({
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
  })),
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
management: defineManagement((role) => ({
  owner: {
    invite: role.any(),
    assignRole: role.any(),
    removeMember: role.any(),
    updateOrg: true,
    deleteOrg: true,
    transferOwnership: true,
  },
}))
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
await client.user.database("notes").from("entries").select()

// Organization database — specify which org
await client.org("acme-corp").database("contacts").from("people").select()

// Global database — no ownership, just the definition name
await client.global("marketplace").from("extensions").select()
```

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
await org.database("contacts").from("people").select()
```

Instead, `client.org("acme-corp")` returns a lightweight handle, not a fetched object. The server validates everything in one request:

```
POST /data/query/people
X-Org: acme-corp
X-Database: contacts
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
import { defineOrg, defineManagement, defineSchema, defineAccess, defineTable, definePolicy, c, r, eq, inList } from "@atomicbase/definitions";

export default defineOrg({
  maxMembers: 50,
  roles: ["owner", "admin", "member", "viewer"],
  management: defineManagement((role) => ({
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
  })),
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

```sql
-- Definitions: schema blueprints with type determining database ownership and access
CREATE TABLE atomicbase_definitions (
    id INTEGER PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    definition_type TEXT NOT NULL CHECK(definition_type IN ('global', 'organization', 'user')),
    roles_json TEXT,  -- NULL for global/user types
    management_json TEXT,  -- NULL for global/user types
    current_version INTEGER DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Databases (pure storage, no ownership concepts)
CREATE TABLE atomicbase_databases (
    id TEXT PRIMARY KEY NOT NULL,
    definition_id INTEGER NOT NULL REFERENCES atomicbase_definitions(id),
    definition_version INTEGER DEFAULT 1,
    token TEXT,  -- Turso connection token
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Users (optionally own one database)
CREATE TABLE atomicbase_users (
    id TEXT PRIMARY KEY NOT NULL,
    database_id TEXT UNIQUE REFERENCES atomicbase_databases(id),
    email TEXT UNIQUE COLLATE NOCASE,
    email_verified_at TEXT,
    phone TEXT,
    phone_verified_at TEXT,
    password_hash TEXT,
    last_sign_in_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Organizations (identity layer on top of databases)
CREATE TABLE atomicbase_organizations (
    id TEXT PRIMARY KEY NOT NULL,
    database_id TEXT NOT NULL UNIQUE REFERENCES atomicbase_databases(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    owner_id TEXT NOT NULL REFERENCES atomicbase_users(id),
    max_members INTEGER,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Membership (on organizations, not databases)
CREATE TABLE atomicbase_membership (
    organization_id TEXT NOT NULL REFERENCES atomicbase_organizations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES atomicbase_users(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(organization_id, user_id)
);

-- Invitations (on organizations)
CREATE TABLE atomicbase_invitations (
    id TEXT PRIMARY KEY NOT NULL,
    organization_id TEXT NOT NULL REFERENCES atomicbase_organizations(id) ON DELETE CASCADE,
    email TEXT NOT NULL COLLATE NOCASE,
    role TEXT NOT NULL,
    invited_by TEXT NOT NULL REFERENCES atomicbase_users(id),
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(organization_id, email)
);

-- Sessions
CREATE TABLE atomicbase_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    secret_hash BLOB NOT NULL,
    user_id TEXT NOT NULL REFERENCES atomicbase_users(id) ON DELETE CASCADE,
    mfa_verified INTEGER NOT NULL DEFAULT 0,
    last_verified_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Schema + access snapshots per version
CREATE TABLE atomicbase_definitions_history (
    id INTEGER PRIMARY KEY,
    definition_id INTEGER NOT NULL REFERENCES atomicbase_definitions(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    schema_json TEXT NOT NULL,
    access_json TEXT NOT NULL,
    checksum TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(definition_id, version)
);

-- Migration SQL between versions
CREATE TABLE atomicbase_migrations (
    id INTEGER PRIMARY KEY,
    definition_id INTEGER NOT NULL REFERENCES atomicbase_definitions(id) ON DELETE CASCADE,
    from_version INTEGER NOT NULL,
    to_version INTEGER NOT NULL,
    sql TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(definition_id, from_version, to_version)
);

-- Migration failures for debugging
CREATE TABLE atomicbase_migration_failures (
    database_id TEXT PRIMARY KEY REFERENCES atomicbase_databases(id) ON DELETE CASCADE,
    from_version INTEGER NOT NULL,
    to_version INTEGER NOT NULL,
    error TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

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
X-Database: <database_id>               # Target database for data requests
X-Organization: <organization_id>       # Target organization (for org databases)
```

**Database resolution:**
- Global: `X-Database` = definition name (auto-created, one per definition)
- User: `X-Database` = user's database ID (from `atomicbase_users.database_id`)
- Organization: `X-Organization` = organization ID (database resolved via `atomicbase_organizations.database_id`)

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
2. Create `api/platform/membership.go` - user-database membership with roles

### Phase 4: Schema Versioning
1. Implement `atomicbase_definitions_history` population on push
2. Implement `atomicbase_migrations` for migration SQL generation
3. Implement lazy migration on database access (check `definition_version`)
4. Track failures in `atomicbase_migration_failures`

### Phase 5: Auth Context
1. Update `api/tools/auth.go` with session-based auth context
2. Membership role lookup from `atomicbase_membership`
3. Inject access conditions into queries based on access rules

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
- `api/platform/membership.go` - User-database membership
- `api/platform/users.go` - User management
- `api/platform/sessions.go` - Session management

## Verification

### Organization Flow
1. Create a `.org.ts` file with schema, roles, management, and access
2. Push via CLI - verify definition created with version in `atomicbase_definitions`
3. Verify `atomicbase_definitions_history` has schema + access JSON
4. Create database via API - verify entry in `atomicbase_databases` with `owner_id` set
5. Add user membership with role via `atomicbase_membership`
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
3. Verify failure logged in `atomicbase_migration_failures`
