import { existsSync, readdirSync } from "node:fs";
import { resolve, basename } from "node:path";
import { createJiti } from "jiti";
import type { DefinitionDefinition } from "@atomicbase/definitions";

const jiti = createJiti(import.meta.url);

export async function loadSchema(filePath: string): Promise<DefinitionDefinition> {
  const absolutePath = resolve(process.cwd(), filePath);

  if (!existsSync(absolutePath)) {
    throw new Error(`Definition file not found: ${filePath}`);
  }

  try {
    const module = await jiti.import(absolutePath);
    const definition = (module as { default?: DefinitionDefinition }).default ?? module as DefinitionDefinition;

    if (definition === undefined) {
      const fileName = basename(filePath);
      throw new Error(
        `No default export in ${fileName}\n\n` +
        `  Your file must export a default definition:\n\n` +
        `    export default defineGlobal({\n` +
        `      schema: defineSchema({ /* tables */ }),\n` +
        `      access: defineAccess({ /* policies */ }),\n` +
        `    });\n`
      );
    }

    if (typeof definition !== "object" || definition === null) {
      throw new Error(`Invalid default export in ${basename(filePath)}\n\n  Expected a definition object.\n`);
    }

    const fileName = basename(filePath);
    const inferredName = deriveDefinitionName(fileName);

    if (typeof definition.type !== "string" || !["global", "user", "organization"].includes(definition.type)) {
      throw new Error(
        `Invalid definition type in ${fileName}\n\n` +
        `  Expected defineGlobal(...), defineUser(...), or defineOrg(...).\n`
      );
    }

    if (!definition.schema || !Array.isArray(definition.schema.tables)) {
      throw new Error(
        `Invalid schema in ${fileName}\n\n` +
        `  Expected: schema: defineSchema({ ... })\n`
      );
    }

    if (typeof definition.access !== "object" || definition.access === null) {
      throw new Error(
        `Invalid access block in ${fileName}\n\n` +
        `  Expected: access: defineAccess({ ... })\n`
      );
    }

    if (definition.name !== undefined && definition.name.trim() === "") {
      throw new Error(
        `Definition in ${fileName} has an empty name\n\n` +
        `  Omit the name to infer it from the filename, or provide a non-empty string.\n`
      );
    }

    if (definition.type === "organization" && definition.roles && definition.roles.length === 0) {
      throw new Error(
        `Organization definition in ${fileName} has an empty roles array\n\n` +
        `  Remove roles or provide at least one role.\n`
      );
    }

    if (definition.schema.tables.length === 0) {
      throw new Error(
        `Definition "${definition.name ?? inferredName}" in ${fileName} has no tables\n\n` +
        `  A definition schema must have at least one table.\n`
      );
    }

    for (const table of definition.schema.tables) {
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

    return {
      ...definition,
      name: definition.name ?? inferredName,
    };
  } catch (err) {
    if (err instanceof Error && (
      err.message.includes("No default export") ||
      err.message.includes("Invalid definition type") ||
      err.message.includes("Invalid schema") ||
      err.message.includes("Invalid access block") ||
      err.message.includes("has an empty name") ||
      err.message.includes("has an empty roles array") ||
      err.message.includes("has no tables") ||
      err.message.includes("has no columns")
    )) {
      throw err;
    }

    const fileName = basename(filePath);

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
          `Invalid definition expression in ${fileName}\n\n` +
          `  Check your defineGlobal/defineUser/defineOrg, defineSchema, and policy helpers.\n`
        );
      }

      throw new Error(`Failed to load ${fileName}: ${err.message}`);
    }
    throw new Error(`Failed to load ${fileName}: ${err}`);
  }
}

function deriveDefinitionName(fileName: string): string {
  return fileName
    .replace(/\.(global|user|org|definition|schema)\.(t|j)s$/, "")
    .replace(/^\+/, "");
}

export function findSchemaFiles(dir: string): string[] {
  const absoluteDir = resolve(process.cwd(), dir);

  if (!existsSync(absoluteDir)) {
    return [];
  }

  const files = readdirSync(absoluteDir);
  return files
    .filter((f) =>
      f.endsWith(".schema.ts") ||
      f.endsWith(".schema.js") ||
      f.endsWith(".global.ts") ||
      f.endsWith(".global.js") ||
      f.endsWith(".user.ts") ||
      f.endsWith(".user.js") ||
      f.endsWith(".org.ts") ||
      f.endsWith(".org.js") ||
      f.endsWith(".definition.ts") ||
      f.endsWith(".definition.js")
    )
    .map((f) => resolve(absoluteDir, f));
}

export async function loadAllSchemas(dir: string): Promise<DefinitionDefinition[]> {
  const files = findSchemaFiles(dir);
  const schemas: DefinitionDefinition[] = [];

  for (const file of files) {
    const schema = await loadSchema(file);
    schemas.push(schema);
  }

  return schemas;
}
