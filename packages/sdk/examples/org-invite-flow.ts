import { createClient } from "@atomicbase/sdk";

const ownerClient = createClient({
  url: "http://localhost:8080",
  sessionToken: process.env.OWNER_SESSION_TOKEN,
});

const invitedClient = createClient({
  url: "http://localhost:8080",
  sessionToken: process.env.INVITED_SESSION_TOKEN,
});

async function inviteFlow() {
  // 1. Owner or admin invites a user by email.
  const createdInvite = await ownerClient.orgs.createInvite("acme", {
    email: "new-user@example.com",
    role: "viewer",
  });
  if (createdInvite.error) {
    throw createdInvite.error;
  }

  // 2. In a real product, the invite email would carry the org id and invite id.
  const inviteIdFromEmail = createdInvite.data.id;

  // 3. The invited user accepts the invite from that emailed link context.
  const accepted = await invitedClient.orgs.acceptInvite("acme", inviteIdFromEmail);
  if (accepted.error) {
    throw accepted.error;
  }

  // 4. The new member can now query the org database.
  const orgData = await invitedClient
    .database("org:acme")
    .from("projects")
    .select("id", "name")
    .limit(10);

  if (orgData.error) {
    throw orgData.error;
  }

  return {
    invite: createdInvite.data,
    member: accepted.data,
    orgData: orgData.data,
  };
}

void inviteFlow();
