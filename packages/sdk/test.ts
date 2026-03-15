import { createClient, eq, inList, isNull, or } from "./src/index.js";

const baseUrl = "http://localhost:8080";
const apiKey = process.env.ATOMICBASE_API_KEY;
const sessionToken = process.env.ATOMICBASE_SESSION_TOKEN;

const serviceClient = createClient({
  url: baseUrl,
  ...(apiKey ? { apiKey } : {}),
});

async function main() {
  // Platform: create a definition with a service key.
  await serviceClient.definitions.create({
    name: "sdk-example",
    type: "global",
    schema: {
      tables: [
        {
          name: "contacts",
          pk: ["id"],
          columns: {
            id: { name: "id", type: "INTEGER" },
            email: { name: "email", type: "TEXT", notNull: true },
            name: { name: "name", type: "TEXT" },
            deleted_at: { name: "deleted_at", type: "TEXT" },
            status: { name: "status", type: "TEXT" },
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

  await serviceClient.databases.create({
    id: "sdk-example-db",
    definition: "sdk-example",
  });

  const globalDb = serviceClient.database("global:sdk-example-db");
  await globalDb.from("contacts").insert({
    id: 1,
    email: "alice@example.com",
    name: "Alice",
    status: "active",
  });

  const globalRows = await globalDb
    .from("contacts")
    .select("id", "email", "name")
    .where(
      or(eq("status", "active"), inList("status", ["trial", "invited"])),
      isNull("deleted_at"),
    )
    .orderBy("id", "asc");

  console.log("global rows", globalRows);

  if (sessionToken) {
    const sessionClient = serviceClient.withSession(sessionToken);

    // Auth: fetch current user.
    const me = await sessionClient.auth.me();
    console.log("me", me);

    // User database: no Database header, routes to the caller's own DB.
    const ownDb = sessionClient.database();
    const profile = await ownDb.from("profile").select().maybeSingle();
    console.log("profile", profile);

    // Organization auth surface.
    const orgs = await sessionClient.orgs.list();
    console.log("orgs", orgs);
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
