# React Todo Example

This is the official Atomicbase todo example now.

It demonstrates the intended product path:

- browser-only client
- built-in Atomicbase magic-link auth
- session token stored in the browser
- direct SDK calls from the app to the Atomicbase API
- organization creation and selection through `/auth/orgs`
- org-scoped data access through `client.database("org:<id>")`
- definitions-first schema, membership, and access control

## What it includes

- an organization definition in [definitions/todo.org.ts](/Users/joeervin/Desktop/atomicbase/examples/react-todo/definitions/todo.org.ts)
- a dedicated app callback route at `/auth/callback`
- direct browser completion of `completeMagicLink(token)`
- direct organization creation with the auth API
- todo CRUD through `client.database("org:<id>")`
- member and invite management through `client.orgs.*`

## Required environment

Create `.env.local` in this app with:

```env
NEXT_PUBLIC_ATOMICBASE_URL=http://localhost:8080
NEXT_PUBLIC_ATOMICBASE_ORG_DEFINITION=todo-team
```

On the Atomicbase API side, make sure:

```env
AUTH_MAGIC_LINK_CALLBACK_URL=http://localhost:3000/auth/callback
AUTH_INVITE_CALLBACK_URL=http://localhost:3000/invite
APP_URL=http://localhost:3000
```

## Push the definition

From this example directory:

```bash
pnpm exec atomicbase definitions push todo-team
```

Or from the repo root:

```bash
pnpm --filter @atomicbase/cli exec atomicbase definitions push todo-team --cwd examples/react-todo
```

## Run the app

From the repo root:

```bash
pnpm dev:react-todo
```

Then:

1. enter your email
2. open the emailed magic link in the same browser
3. the callback page completes login and stores the session token locally
4. create an organization workspace
5. add shared todos directly against that org database
6. invite another member if you are the owner

## Definition notes

This example intentionally uses the idiomatic org-database pattern:

- no `user_id` column on `todos`
- one database per organization
- membership defined in `defineMembership(...)`
- all todo operations require `auth.status == "member"`

The isolation boundary is the organization database plus tenant-local membership, not a per-row owner column.
