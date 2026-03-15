# React Todo Example (Legacy)

This example is a legacy reference app.

It demonstrates an older Atomicbase path:

- Google OAuth in the app
- server-managed auth/session handling
- template-era provisioning and schema flow
- a pre-definitions browser architecture

It is **not** the intended product direction anymore.

## Status

Use this example only as historical reference while the new definitions-first browser todo app is being built.

The intended official path for new apps is:

- built-in Atomicbase magic-link auth
- session token stored client-side in the browser
- direct browser SDK calls to the Atomicbase API
- user self-provisioning through `POST /auth/me/database`
- `client.database()` for the signed-in user's own database
- definitions, access policies, provisioning rules, and managed migrations

## Why this example is legacy

This app still depends on:

- Google OAuth credentials
- app-owned auth orchestration
- `schemas/` instead of `definitions/`
- template-era provisioning assumptions

That makes it a poor fit for demonstrating the current Atomicbase BaaS model.

## If you still want to run it

You will need:

1. Atomicbase API running
2. Google OAuth credentials
3. The older template-era setup this example was written for

Expect the README, code, and current backend product direction to diverge.
