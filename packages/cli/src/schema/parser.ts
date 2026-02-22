import { existsSync, readdirSync } from "node:fs";
import { resolve, basename } from "node:path";
import { createJiti } from "jiti";
import type { SchemaDefinition } from "@atomicbase/template";

// Create jiti instance for loading TypeScript schema files
const jiti = createJiti(import.meta.url);

/**
 * Load a schema from a .schema.ts file.
 */
export async function loadSchema(filePath: string): Promise<SchemaDefinition> {
  const absolutePath = resolve(process.cwd(), filePath);

  if (!existsSync(absolutePath)) {
    throw new Error(`Schema file not found: ${filePath}`);
  }

  try {
    const module = await jiti.import(absolutePath);
    const schema = (module as { default?: SchemaDefinition }).default ?? module as SchemaDefinition;

    if (schema === undefined) {
      const fileName = basename(filePath);
      throw new Error(
        `No default export in ${fileName}\n\n` +
        `  Your schema file must export a default schema:\n\n` +
        `    export default defineSchema("my-app", {\n` +
        `      // tables...\n` +
        `    });\n`
      );
    }

    if (typeof schema.name !== "string" || !Array.isArray(schema.tables)) {
      const fileName = basename(filePath);
      throw new Error(
        `Invalid default export in ${fileName}\n\n` +
        `  Expected: export default defineSchema(...)\n` +
        `  Got: ${typeof schema === "object" ? JSON.stringify(Object.keys(schema)) : typeof schema}\n`
      );
    }

    const fileName = basename(filePath);

    // Validate schema name
    if (schema.name.trim() === "") {
      throw new Error(
        `Schema in ${fileName} has no name\n\n` +
        `  The first argument to defineSchema must be a non-empty string:\n\n` +
        `    export default defineSchema("my-app", { ... });\n`
      );
    }

    // Validate at least one table
    if (schema.tables.length === 0) {
      throw new Error(
        `Schema "${schema.name}" in ${fileName} has no tables\n\n` +
        `  A schema must have at least one table:\n\n` +
        `    export default defineSchema("${schema.name}", {\n` +
        `      users: defineTable({ ... }),\n` +
        `    });\n`
      );
    }

    // Validate each table has at least one column
    for (const table of schema.tables) {
      if (!table.columns || Object.keys(table.columns).length === 0) {
        throw new Error(
          `Table "${table.name}" in ${fileName} has no columns\n\n` +
          `  Each table must have at least one column:\n\n` +
          `    ${table.name}: defineTable({\n` +
          `      id: c.integer().primaryKey(),\n` +
          `    }),\n`
        );
      }
    }

    return schema as SchemaDefinition;
  } catch (err) {
    // Re-throw our own validation errors
    if (err instanceof Error && (
      err.message.includes("No default export") ||
      err.message.includes("Invalid default export") ||
      err.message.includes("has no name") ||
      err.message.includes("has no tables") ||
      err.message.includes("has no columns")
    )) {
      throw err;
    }

    const fileName = basename(filePath);

    // Handle common schema definition errors
    if (err instanceof Error) {
      if (err.message.includes("_build is not a function")) {
        throw new Error(
          `Invalid table definition in ${fileName}\n\n` +
          `  Tables must be defined using defineTable():\n\n` +
          `    users: defineTable({\n` +
          `      id: c.integer().primaryKey(),\n` +
          `    }),\n`
        );
      }

      if (err.message.includes("is not a function")) {
        throw new Error(
          `Invalid column definition in ${fileName}\n\n` +
          `  Columns must be defined using c.integer(), c.text(), etc:\n\n` +
          `    id: c.integer().primaryKey(),\n` +
          `    name: c.text().notNull(),\n`
        );
      }

      throw new Error(`Failed to load ${fileName}: ${err.message}`);
    }
    throw new Error(`Failed to load ${fileName}: ${err}`);
  }
}

/**
 * Find all schema files in a directory.
 */
export function findSchemaFiles(dir: string): string[] {
  const absoluteDir = resolve(process.cwd(), dir);

  if (!existsSync(absoluteDir)) {
    return [];
  }

  const files = readdirSync(absoluteDir);
  return files
    .filter((f) => f.endsWith(".schema.ts") || f.endsWith(".schema.js"))
    .map((f) => resolve(absoluteDir, f));
}

/**
 * Load all schemas from a directory.
 */
export async function loadAllSchemas(dir: string): Promise<SchemaDefinition[]> {
  const files = findSchemaFiles(dir);
  const schemas: SchemaDefinition[] = [];

  for (const file of files) {
    const schema = await loadSchema(file);
    schemas.push(schema);
  }

  return schemas;
}
