# AtomBase database definition model

## Definition types

There are 3 types of database definitions. Global, org, and user. Global definitions are database definitions tied to a single global database. Org definitions are database definitions tied to organization tenants. User definitions are database definitions tied to user tenants.

## Core differences

There are a few core differences between the database definition types.
1. # of databases. Global: 1. Org: many (1 per org). User: many (1 per user).
2. Schema definitions. Actually the same across all types.
3. Access policies. Global: RLS with auth vs anon context. Org: RLS + RBAC with roles defined per definition. User: RLS only (auth context doesn't matter because user -> db mapping is 1:1).
4. Roles. Global: service, authenticated, anon. Org: any role. User: no roles.

## Schema definitions

Schema definitions are already well defined in the platform package and packages/definitions.

## Access definitions

Access definitions granularly define how databases can be accessed. They use a mix of RLS and RBAC. Service role connections completely bypass access definitions so they are able to run any operation on any database. Access definitions are defined per table and per operation (SELECT, UPDATE, INSERT, DELETE). The context access polices get is dependent on the operation they're performing and the type of definition. Auth context is as follows: For global databases, policies get status (authenticated, anonymous). For org databases, policies get status (member, anonymous) and role (org defined or NULL if not a member of org). For user databases, no auth context is passed. Row context is as follows: SELECT -> (old). UPDATE -> (old, new). INSERT -> (new). DELETE -> (old).

| Operation | `auth` | `old` | `new` |
|-----------|--------|-------|-------|
| SELECT | ✓ | ✓ | — |
| INSERT | ✓ | — | ✓ |
| UPDATE | ✓ | ✓ | ✓ |
| DELETE | ✓ | ✓ | — |

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

## Management

Management is only available for organizations as it defines what parts of the organization a role can manage.
- Keys are role names (must match `roles` array)
- `invite`, `assignRole`, `removeMember` — which target roles this role can manage
  - `role.any()` — can manage all roles
  - `[role.member, role.viewer]` — can only manage specific roles
- `updateOrg`, `deleteOrg`, `transferOwnership` — binary permissions (`true` to allow)

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

## Examples

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

## Storage

All data for global and user database definitions is stored in the primary database and heavily cached. For org databases, membership is stored in the tenant database. This is because membership 
