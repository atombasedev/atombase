import { test } from "node:test";
import assert from "node:assert/strict";

import { createClient, eq } from "../dist/index.js";

const BASE_URL = process.env.ATOMICBASE_CONTRACT_BASE_URL ?? "http://127.0.0.1:8080";
const API_KEY = process.env.ATOMICBASE_API_KEY;
const RUN_CONTRACT = process.env.ATOMICBASE_CONTRACT === "1";

const skipReason = RUN_CONTRACT
  ? null
  : "Set ATOMICBASE_CONTRACT=1 to run live API/SDK contract tests";

function authHeaders() {
  const headers = { "Content-Type": "application/json" };
  if (API_KEY) {
    headers.Authorization = `Bearer ${API_KEY}`;
  }
  return headers;
}

async function apiRequest(path, { method = "GET", body } = {}) {
  const response = await fetch(`${BASE_URL}${path}`, {
    method,
    headers: authHeaders(),
    body: body ? JSON.stringify(body) : undefined,
  });

  if (!response.ok) {
    const errorBody = await response.text();
    throw new Error(`API ${method} ${path} failed (${response.status}): ${errorBody}`);
  }

  const text = await response.text();
  if (!text) return null;
  return JSON.parse(text);
}

async function assertHealthy() {
  const response = await fetch(`${BASE_URL}/health`);
  assert.equal(response.status, 200, "API must be reachable before running contract tests");
}

function buildDefinition(definitionName) {
  return {
    name: definitionName,
    type: "global",
    schema: {
      tables: [
        {
          name: "contacts",
          pk: ["id"],
          columns: {
            id: { name: "id", type: "INTEGER" },
            name: { name: "name", type: "TEXT", notNull: true },
            email: { name: "email", type: "TEXT", notNull: true, unique: true },
            status: { name: "status", type: "TEXT", default: "active" },
          },
        },
      ],
    },
    access: {
      contacts: {
        select: { field: "auth.status", op: "eq", value: "anonymous" },
        insert: { field: "auth.status", op: "eq", value: "anonymous" },
        update: { field: "auth.status", op: "eq", value: "anonymous" },
        delete: { field: "auth.status", op: "eq", value: "anonymous" },
      },
    },
  };
}

test("SDK <-> API contract: core tenant data flows", { skip: skipReason ?? false }, async (t) => {
  await assertHealthy();

  const suffix = Date.now();
  const definitionName = `sdk-contract-${suffix}`;
  const tenantName = `sdk-contract-tenant-${suffix}`;

  const client = createClient({
    url: BASE_URL,
    ...(API_KEY ? { apiKey: API_KEY } : {}),
  });

  let definitionCreated = false;
  let tenantCreated = false;

  try {
    await apiRequest("/platform/definitions", {
      method: "POST",
      body: buildDefinition(definitionName),
    });
    definitionCreated = true;

    const tenantCreate = await client.databases.create({
      id: tenantName,
      definition: definitionName,
    });
    assert.equal(tenantCreate.error, null, `tenant creation failed: ${tenantCreate.error?.message}`);
    tenantCreated = true;

    const tenant = client.database(`global:${tenantName}`);

    await t.test("insert + select + filter flow", async () => {
      const first = await tenant.from("contacts").insert({
        id: 1,
        name: "Alice",
        email: "alice.contract@example.com",
      });
      assert.equal(first.error, null, first.error?.message);

      const second = await tenant.from("contacts").insert({
        id: 2,
        name: "Bob",
        email: "bob.contract@example.com",
        status: "inactive",
      });
      assert.equal(second.error, null, second.error?.message);

      const filtered = await tenant.from("contacts").select("id", "name", "status").where(eq("id", 2)).single();
      assert.equal(filtered.error, null, filtered.error?.message);
      assert.equal(filtered.data?.name, "Bob");
      assert.equal(filtered.data?.status, "inactive");
    });

    await t.test("count and withCount contracts", async () => {
      const countResult = await tenant.from("contacts").select().count();
      assert.equal(countResult.error, null, countResult.error?.message);
      assert.equal(countResult.data, 2);

      const withCountResult = await tenant.from("contacts").select("id").limit(1).withCount();
      assert.equal(withCountResult.error, null, withCountResult.error?.message);
      assert.equal(withCountResult.data?.length, 1);
      assert.equal(withCountResult.count, 2);
    });

    await t.test("update + delete flow", async () => {
      const updateResult = await tenant
        .from("contacts")
        .update({ status: "archived" })
        .where(eq("id", 1));
      assert.equal(updateResult.error, null, updateResult.error?.message);
      assert.equal(updateResult.data?.rows_affected, 1);

      const verifyUpdate = await tenant
        .from("contacts")
        .select("status")
        .where(eq("id", 1))
        .single();
      assert.equal(verifyUpdate.error, null, verifyUpdate.error?.message);
      assert.equal(verifyUpdate.data?.status, "archived");

      const deleteResult = await tenant.from("contacts").delete().where(eq("id", 2));
      assert.equal(deleteResult.error, null, deleteResult.error?.message);
      assert.equal(deleteResult.data?.rows_affected, 1);

      const remaining = await tenant.from("contacts").select().count();
      assert.equal(remaining.error, null, remaining.error?.message);
      assert.equal(remaining.data, 1);
    });

    await t.test("batch atomic rollback contract", async () => {
      const before = await tenant.from("contacts").select().count();
      assert.equal(before.error, null, before.error?.message);

      const batchResult = await tenant.batch([
        tenant.from("contacts").insert({
          id: 10,
          name: "Should Rollback",
          email: "rollback.contract@example.com",
        }),
        tenant.from("contacts").insert({
          id: 11,
          name: "Duplicate Email",
          email: "alice.contract@example.com",
        }),
      ]);
      assert.notEqual(batchResult.error, null, "batch should fail to verify transaction rollback");

      const after = await tenant.from("contacts").select().count();
      assert.equal(after.error, null, after.error?.message);
      assert.equal(after.data, before.data, "row count should be unchanged after failed batch");
    });

    await t.test("error code contract for unsafe update", async () => {
      const unsafeUpdate = await tenant.from("contacts").update({ status: "bad" });
      assert.notEqual(unsafeUpdate.error, null, "update without where should fail");
      assert.equal(unsafeUpdate.error?.code, "MISSING_WHERE_CLAUSE");
    });
  } finally {
    if (tenantCreated) {
      const deleteTenant = await client.databases.delete(tenantName);
      if (deleteTenant.error && deleteTenant.error.code !== "DATABASE_NOT_FOUND") {
        throw new Error(`failed to clean up tenant ${tenantName}: ${deleteTenant.error.message}`);
      }
    }
    if (definitionCreated) {
      // Definition delete endpoint does not exist yet; leave cleanup to disposable test names.
    }
  }
});
