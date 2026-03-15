# Atomicbase SDK Design

## Current model

The SDK mirrors the current Atomicbase API split:

- `client.definitions`
  service-key platform definition management
- `client.databases`
  service-key platform database management
- `client.auth`
  session-backed auth flows plus user self-provisioning
- `client.orgs`
  convenience alias for `client.auth.orgs`
- `client.database(...)`
  data API access

## Auth modes

The SDK supports two caller modes:

- service mode via `apiKey`
- session mode via `sessionToken`

Service mode is used for:

- `/platform/*`
- admin/service auth operations

Session mode is used for:

- `/auth/me`
- `/auth/me/database`
- session-backed org operations
- data access against the caller's own database

## Database routing

The data API has three routing modes:

- `client.database()`
  no `Database` header, resolves to the current authenticated user's database
- `client.database("global:<database-id>")`
  explicit global database routing
- `client.database("org:<organization-id>")`
  explicit organization database routing

There is no longer a `user:<definition-name>` routing mode.

## Definitions client

`client.definitions` exposes:

- `list()`
- `get(name)`
- `create(payload)`
- `push(name, payload)`
- `history(name)`

Definitions are now definition-first, not template-first. The SDK types include:

- `Definition`
- `DefinitionVersion`
- `CreateDefinitionOptions`
- `PushDefinitionOptions`

## Auth client

`client.auth` exposes:

- `startMagicLink({ email })`
- `completeMagicLink(token)`
- `signOut()`
- `me()`
- `createDatabase({ definition })`

`createDatabase` is the self-service user provisioning flow. It is session-backed and subject to definition provisioning rules.

## Organization client

`client.orgs` exposes:

- orgs:
  - `list()`
  - `create(...)`
  - `get(orgId)`
  - `update(orgId, ...)`
  - `delete(orgId)`
  - `transferOwnership(orgId, ...)`
- members:
  - `listMembers(orgId)`
  - `addMember(orgId, ...)`
  - `updateMember(orgId, userId, ...)`
  - `removeMember(orgId, userId)`
- invites:
  - `listInvites(orgId)`
  - `createInvite(orgId, ...)`
  - `deleteInvite(orgId, inviteId)`
  - `acceptInvite(orgId, inviteId)`

## Query builder

`client.database(...).from(table)` returns the query builder.

Supported operations:

- `select`
- `insert`
- `upsert`
- `update`
- `delete`
- `batch`

Supported result modifiers:

- `single()`
- `maybeSingle()`
- `count()`
- `withCount()`

Supported filter helpers:

- `eq`, `neq`
- `gt`, `gte`, `lt`, `lte`
- `like`, `glob`
- `inList`, `notInList`
- `between`
- `isNull`, `isNotNull`
- `fts`
- `and`, `or`, `not`

## Design constraints

- The SDK should not invent semantics that differ from the API.
- Session vs service auth should be explicit and composable.
- The default data client should support the user-self path cleanly.
- Organization management belongs under auth, not platform.
- Definitions, not templates, are the canonical platform abstraction.
