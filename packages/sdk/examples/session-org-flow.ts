import { createClient, eq } from "@atomicbase/sdk";

const baseClient = createClient({
  url: "http://localhost:8080",
});

async function sessionOrgFlow(email: string, magicLinkToken: string) {
  // 1. Start login by email.
  await baseClient.auth.startMagicLink({ email });

  // 2. Complete login from the emailed token and derive a session client.
  const completed = await baseClient.auth.completeMagicLink(magicLinkToken);
  if (completed.error) {
    throw completed.error;
  }

  const client = baseClient.withSession(completed.data.token);

  // 3. Check the current user.
  const me = await client.auth.me();
  if (me.error) {
    throw me.error;
  }

  // 4. Provision the caller's user database if they do not have one yet.
  if (!me.data.databaseId) {
    const provisioned = await client.auth.createDatabase({
      definition: "workspace",
    });
    if (provisioned.error) {
      throw provisioned.error;
    }
  }

  // 5. Query the caller's own database. No Database header is needed.
  const ownProjects = await client
    .database()
    .from("projects")
    .select("id", "name")
    .where(eq("status", "active"));

  if (ownProjects.error) {
    throw ownProjects.error;
  }

  // 6. Create an organization through the auth API.
  const org = await client.orgs.create({
    id: "acme",
    name: "Acme",
    definition: "org-workspace",
  });
  if (org.error) {
    throw org.error;
  }

  // 7. Query the organization's tenant database explicitly.
  const orgTasks = await client
    .database(`org:${org.data.id}`)
    .from("tasks")
    .select("id", "title")
    .limit(10);

  if (orgTasks.error) {
    throw orgTasks.error;
  }

  return {
    user: me.data,
    ownProjects: ownProjects.data,
    organization: org.data,
    orgTasks: orgTasks.data,
  };
}

const tokenFromEmail = new URL(window.location.href).searchParams.get("token");

if (!tokenFromEmail) {
  throw new Error("Missing magic-link token");
}

void sessionOrgFlow("joe@example.com", tokenFromEmail);
