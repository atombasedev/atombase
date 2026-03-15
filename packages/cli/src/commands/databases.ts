import { Command } from "commander";
import { createInterface } from "readline";
import { loadConfig } from "../config.js";
import { ApiClient, ApiError } from "../api.js";

async function confirm(question: string): Promise<boolean> {
  const rl = createInterface({
    input: process.stdin,
    output: process.stdout,
  });

  return new Promise((resolve) => {
    rl.question(`${question} [Y/n] `, (answer) => {
      rl.close();
      const normalized = answer.trim().toLowerCase();
      resolve(normalized === "" || normalized === "y" || normalized === "yes");
    });
  });
}

function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleString();
}

async function listDatabases(): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  try {
    const databases = await api.listDatabases();
    if (databases.length === 0) {
      console.log("No databases found.");
      console.log("\nCreate one with: atomicbase databases create <id> --definition <definition>");
      return;
    }

    console.log("Databases:\n");
    console.log("  ID                   DEFINITION           TYPE           VERSION    CREATED");
    console.log("  " + "-".repeat(92));

    for (const database of databases) {
      console.log(
        `  ${database.id.padEnd(20)} ${(database.definitionName ?? "").padEnd(20)} ${(database.definitionType ?? "").padEnd(14)} ${String(database.definitionVersion).padEnd(10)} ${formatDate(database.createdAt)}`
      );
    }

    console.log(`\n  Total: ${databases.length} database(s)`);
  } catch (err) {
    console.error("Failed to list databases:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

async function getDatabase(id: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  try {
    const database = await api.getDatabase(id);
    console.log(`Database: ${database.id}\n`);
    console.log(`  ID:                 ${database.id}`);
    console.log(`  Definition ID:      ${database.definitionId}`);
    console.log(`  Definition Name:    ${database.definitionName ?? ""}`);
    console.log(`  Definition Type:    ${database.definitionType ?? ""}`);
    console.log(`  Definition Version: ${database.definitionVersion}`);
    console.log(`  Created:            ${formatDate(database.createdAt)}`);
    console.log(`  Updated:            ${formatDate(database.updatedAt)}`);
    if (database.organizationId) {
      console.log(`  Organization ID:    ${database.organizationId}`);
    }
    if (database.organizationName) {
      console.log(`  Organization Name:  ${database.organizationName}`);
    }
    if (database.ownerId) {
      console.log(`  Owner ID:           ${database.ownerId}`);
    }
    if (database.token) {
      console.log(`  Token:              ${database.token}`);
    }
  } catch (err) {
    console.error("Failed to get database:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

async function createDatabase(
  id: string,
  options: {
    definition: string;
    userId?: string;
    organizationId?: string;
    organizationName?: string;
    ownerId?: string;
    maxMembers?: string;
  }
): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  console.log(`Creating database "${id}" from definition "${options.definition}"...`);

  try {
    const database = await api.createDatabase({
      id,
      definition: options.definition,
      userId: options.userId,
      organizationId: options.organizationId,
      organizationName: options.organizationName,
      ownerId: options.ownerId,
      maxMembers: options.maxMembers ? Number(options.maxMembers) : undefined,
    });

    console.log(`\nCreated database "${database.id}"`);
    console.log(`  Definition:         ${database.definitionName ?? options.definition}`);
    console.log(`  Definition Type:    ${database.definitionType ?? ""}`);
    console.log(`  Definition Version: ${database.definitionVersion}`);
    if (database.organizationId) {
      console.log(`  Organization ID:    ${database.organizationId}`);
    }
    if (database.token) {
      console.log(`  Token:              ${database.token}`);
    }
  } catch (err) {
    console.error("\nFailed to create database:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

async function deleteDatabase(id: string, force: boolean): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  if (!force) {
    try {
      await api.getDatabase(id);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        console.error(`Database "${id}" not found.`);
        process.exit(1);
      }
      throw err;
    }

    if (!(await confirm(`Are you sure you want to delete database "${id}"? This action cannot be undone.`))) {
      console.log("Aborted.");
      process.exit(0);
    }
  }

  console.log(`Deleting database "${id}"...`);
  try {
    await api.deleteDatabase(id);
    console.log(`Deleted database "${id}"`);
  } catch (err) {
    console.error("Failed to delete database:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

export const databasesCommand = new Command("databases")
  .description("Manage databases");

databasesCommand
  .command("list")
  .alias("ls")
  .description("List all databases")
  .action(listDatabases);

databasesCommand
  .command("get <id>")
  .description("Get database details")
  .action(getDatabase);

databasesCommand
  .command("create <id>")
  .description("Create a new database from a definition")
  .requiredOption("-d, --definition <definition>", "Definition name to use")
  .option("--user-id <userId>", "User id for user definitions")
  .option("--organization-id <organizationId>", "Organization id for organization definitions")
  .option("--organization-name <organizationName>", "Organization name for organization definitions")
  .option("--owner-id <ownerId>", "Owner id for organization definitions")
  .option("--max-members <maxMembers>", "Max members for organization definitions")
  .action((id: string, options) => {
    createDatabase(id, options);
  });

databasesCommand
  .command("delete <id>")
  .alias("rm")
  .description("Delete a database")
  .option("-f, --force", "Skip confirmation prompt")
  .action((id: string, options: { force?: boolean }) => {
    deleteDatabase(id, options.force ?? false);
  });
