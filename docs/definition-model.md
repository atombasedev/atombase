# Atomicbase Definition Model

## Overview

Atomicbase definitions describe:

- schema
- access policies
- provisioning rules
- organization membership and management rules

There are three definition types:

- `global`
- `user`
- `organization`

All three share the same schema authoring model. The difference is how databases are provisioned and how auth context is applied at runtime.

## Definition types

### Global

- Many databases can use the same global definition.
- Data requests target them with `Database: global:<database-id>`.
- Access policies usually reason about `auth.status` and optionally `auth.id`.

### User

- Each user can have one optional user database.
- Browser and SDK clients access it by omitting the `Database` header and calling `client.database()`.
- Self-service provisioning is controlled by the definition's `provision` rule.

### Organization

- Each organization has one required tenant database.
- Data requests target it with `Database: org:<organization-id>`.
- Membership is tenant-local, and org roles plus management rules come from the definition.

## Schema authoring

Definitions are authored with `@atomicbase/definitions`.

```ts
import {
  allow,
  c,
  defineAccess,
  defineSchema,
  defineTable,
  defineUser,
} from "@atomicbase/definitions";

const schema = defineSchema({
  todos: defineTable({
    id: c.integer().primaryKey(),
    title: c.text().notNull(),
    completed: c.integer().notNull().default(0),
    created_at: c.text().notNull(),
    deleted_at: c.text(),
  }),
});

export default defineUser({
  schema,
  access: defineAccess(schema, {
    todos: {
      select: allow(),
      insert: allow(),
      update: allow(),
      delete: allow(),
    },
  }),
});
```

Available schema features include:

- `primaryKey`
- `notNull`
- `unique`
- `default`
- `check`
- `generatedAs`
- `references`
- `index` / `uniqueIndex`
- `fts`

## Access policies

Access policies are defined per table and per operation:

- `select`
- `insert`
- `update`
- `delete`

They are authored with `defineAccess(schema, ...)`.

### Policy context

| Operation | `auth` | `prev` | `next` |
| --- | --- | --- | --- |
| `select` | yes | yes | no |
| `insert` | yes | no | yes |
| `update` | yes | yes | yes |
| `delete` | yes | yes | no |

Use `prev` for the existing row state and `next` for the proposed row state.

```ts
import {
  allow,
  and,
  defineAccess,
  defineSchema,
  defineTable,
  defineUser,
  eq,
  isNull,
  c,
} from "@atomicbase/definitions";

const schema = defineSchema({
  todos: defineTable({
    id: c.integer().primaryKey(),
    owner_id: c.text().notNull(),
    title: c.text().notNull(),
    deleted_at: c.text(),
  }),
});

export default defineUser({
  schema,
  access: defineAccess(schema, {
    todos: {
      select: ({ auth, prev }) =>
        and(
          eq(prev.owner_id, auth.id),
          isNull(prev.deleted_at),
        ),
      insert: ({ auth, next }) => eq(next.owner_id, auth.id),
      update: ({ auth, prev, next }) =>
        and(
          eq(prev.owner_id, auth.id),
          eq(next.owner_id, auth.id),
        ),
      delete: ({ auth, prev }) => eq(prev.owner_id, auth.id),
    },
  }),
});
```

### Auth fields

Current access authoring uses:

- `auth.id`
- `auth.status`
- `auth.role` for organization definitions

`auth.role` is only valid for organization definitions.

## Provisioning rules

`user` and `organization` definitions can declare a `provision` rule. `global` definitions cannot.

Provisioning rules are deny-by-default for self-service flows. If `provision` is omitted, self-service provisioning is rejected.

Current provisioning context supports:

- `auth.status`
- `auth.id`
- `auth.email`
- `auth.verified`

```ts
import {
  defineProvision,
  defineUser,
  defineSchema,
  defineTable,
  defineAccess,
  allow,
  eq,
  c,
} from "@atomicbase/definitions";

const schema = defineSchema({
  todos: defineTable({
    id: c.integer().primaryKey(),
    title: c.text().notNull(),
  }),
});

export default defineUser({
  provision: defineProvision(({ auth }) => eq(auth.verified, true)),
  schema,
  access: defineAccess(schema, {
    todos: {
      select: allow(),
      insert: allow(),
      update: allow(),
      delete: allow(),
    },
  }),
});
```

## Organization membership and management

Organization definitions define membership with `defineMembership({ roles, management })`.

```ts
import {
  allow,
  defineAccess,
  defineMembership,
  defineOrg,
  defineSchema,
  defineTable,
  inList,
  c,
} from "@atomicbase/definitions";

const schema = defineSchema({
  projects: defineTable({
    id: c.integer().primaryKey(),
    name: c.text().notNull(),
    created_by: c.text().notNull(),
  }),
});

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
  schema,
  access: defineAccess(schema, {
    projects: {
      select: allow(),
      insert: ({ auth }) => inList(auth.role, ["member", "admin", "owner"]),
      delete: ({ auth }) => inList(auth.role, ["admin", "owner"]),
    },
  }),
});
```

Management controls:

- `invite`
- `assignRole`
- `removeMember`
- `updateOrg`
- `deleteOrg`
- `transferOwnership`

## Browser app path

The intended browser-app flow for a user definition is:

1. complete magic-link auth
2. store the returned session token in `localStorage`
3. restore that token on app boot
4. create a session-scoped SDK client
5. call `client.auth.me()`
6. self-provision with `client.auth.createDatabase({ definition })` if the user has no database yet
7. query with `client.database()` directly from the browser

That is the main Atomicbase BaaS path. Security comes from session auth plus definition-driven access and provisioning policies, not from hiding the API behind a custom app backend.
