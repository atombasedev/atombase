# @atomicbase/sdk

TypeScript SDK for the Atomicbase API.

## Install

```bash
pnpm add @atomicbase/sdk
```

## Create a client

Service client:

```ts
import { createClient } from "@atomicbase/sdk";

const client = createClient({
  url: "http://localhost:8080",
  apiKey: process.env.ATOMICBASE_API_KEY,
});
```

Session client:

```ts
import { createClient } from "@atomicbase/sdk";

const client = createClient({
  url: "http://localhost:8080",
  sessionToken,
});
```

Or derive a session-scoped client from a service-scoped base client:

```ts
const sessionClient = client.withSession(sessionToken);
```

For the official browser-app path, persist the session token client-side, restore it on app boot, and clear it on sign-out. The example flow uses `localStorage` for that lifecycle because it is simple and explicit:

```ts
const storageKey = "atombase.session";

export function loadSessionToken() {
  return localStorage.getItem(storageKey);
}

export function saveSessionToken(token: string) {
  localStorage.setItem(storageKey, token);
}

export function clearSessionToken() {
  localStorage.removeItem(storageKey);
}
```

## Data access

User database, using the authenticated caller's own database:

```ts
const me = client.database();

const { data, error } = await me
  .from("profile")
  .select("id", "email")
  .single();
```

Global database:

```ts
const publicCatalog = client.database("global:public-catalog-prod");
```

Organization database:

```ts
const orgDb = client.database("org:org_123");
```

## Platform clients

Definitions:

```ts
const created = await client.definitions.create({
  name: "workspace",
  type: "global",
  schema: {
    tables: [
      {
        name: "contacts",
        pk: ["id"],
        columns: {
          id: { name: "id", type: "INTEGER" },
          name: { name: "name", type: "TEXT", notNull: true },
        },
      },
    ],
  },
  access: {
    contacts: {
      select: { field: "auth.status", op: "eq", value: "anonymous" },
    },
  },
});
```

Databases:

```ts
const { data: database } = await client.databases.create({
  id: "workspace-prod",
  definition: "workspace",
});
```

## Auth client

Magic link:

```ts
const auth = client.auth;

await auth.startMagicLink({ email: "joe@example.com" });
const completed = await auth.completeMagicLink(tokenFromEmail);

if (!completed.error) {
  const sessionClient = client.withSession(completed.data.token);
}
```

Current user:

```ts
const { data: user } = await client.auth.me();
```

Provision the current user's database:

```ts
await client.auth.createDatabase({
  definition: "workspace",
});
```

Full session-based app flow:
- [packages/sdk/examples/session-org-flow.ts](/Users/joeervin/Desktop/atomicbase/packages/sdk/examples/session-org-flow.ts)

## Organizations

Create and manage orgs through auth:

```ts
const { data: org } = await client.orgs.create({
  id: "acme",
  name: "Acme",
  definition: "org-workspace",
});
```

Members:

```ts
await client.orgs.addMember("acme", {
  userId: "user_123",
  role: "member",
});
```

Invites:

```ts
await client.orgs.createInvite("acme", {
  email: "new-user@example.com",
  role: "viewer",
});

await client.orgs.acceptInvite("acme", "invite_123");
```

Ownership transfer:

```ts
await client.orgs.transferOwnership("acme", {
  userId: "user_456",
});
```

Invite and acceptance flow:
- [packages/sdk/examples/org-invite-flow.ts](/Users/joeervin/Desktop/atomicbase/packages/sdk/examples/org-invite-flow.ts)

## End-to-end patterns

Signed-in app flow:

```ts
const baseClient = createClient({ url: "http://localhost:8080" });

await baseClient.auth.startMagicLink({ email: "joe@example.com" });
const completed = await baseClient.auth.completeMagicLink(tokenFromEmail);
if (completed.error) throw completed.error;

const client = baseClient.withSession(completed.data.token);

const me = await client.auth.me();
if (me.error) throw me.error;

if (!me.data.databaseId) {
  const provisioned = await client.auth.createDatabase({
    definition: "workspace",
  });
  if (provisioned.error) throw provisioned.error;
}

const ownDb = client.database();
const org = await client.orgs.create({
  id: "acme",
  name: "Acme",
  definition: "org-workspace",
});
if (org.error) throw org.error;

const orgDb = client.database(`org:${org.data.id}`);
```

Invite acceptance flow:

```ts
const ownerClient = createClient({
  url: "http://localhost:8080",
  sessionToken: ownerSessionToken,
});

const invitedClient = createClient({
  url: "http://localhost:8080",
  sessionToken: invitedSessionToken,
});

const invite = await ownerClient.orgs.createInvite("acme", {
  email: "new-user@example.com",
  role: "viewer",
});
if (invite.error) throw invite.error;

const accepted = await invitedClient.orgs.acceptInvite("acme", invite.data.id);
if (accepted.error) throw accepted.error;
```

## Query helpers

```ts
import { createClient, eq, and, isNull, inList } from "@atomicbase/sdk";

const client = createClient({ url: "http://localhost:8080", sessionToken });

const { data } = await client
  .database()
  .from("tasks")
  .select("id", "title")
  .where(
    and(
      eq("status", "open"),
      isNull("deleted_at"),
    ),
    inList("priority", ["high", "urgent"]),
  )
  .orderBy("created_at", "desc")
  .limit(20);
```

## Notes

- `client.database()` with no argument targets the current user's database.
- `client.database("global:<database-id>")` targets a global database.
- `client.database("org:<organization-id>")` targets an organization database.
- Service-key platform calls use `apiKey`.
- Session-backed auth and user data calls use `sessionToken`.
- The browser-first example path stores the session token client-side, restores it on app boot, and clears it on sign-out.
- The intended security model is direct browser API use backed by session auth plus definition-driven access and provisioning policies.
