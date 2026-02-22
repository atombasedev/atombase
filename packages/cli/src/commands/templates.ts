import { Command } from "commander";
import { createInterface } from "readline";
import { writeFileSync, readFileSync, mkdirSync, existsSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { loadConfig } from "../config.js";
import { loadSchema, loadAllSchemas } from "../schema/parser.js";
import { ApiClient, ApiError, type SchemaDiff, type Merge, type TemplateResponse } from "../api.js";
import type { SchemaDefinition, ColumnDefinition, ForeignKeyAction, Collation } from "@atomicbase/template";

// =============================================================================
// Shared Utilities
// =============================================================================

/**
 * Prompt user with a yes/no question.
 */
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

/**
 * Format a date string for display.
 */
function formatDate(dateStr: string): string {
  const date = new Date(dateStr);
  return date.toLocaleString();
}

// =============================================================================
// Push Command Helpers
// =============================================================================

/**
 * Ambiguous change detected client-side: a drop + add pair that might be a rename.
 */
interface AmbiguousChange {
  type: "table" | "column";
  table: string;
  column?: string;
  dropIndex: number;
  addIndex: number;
}

interface DestructiveChange {
  type: "drop_table" | "drop_column";
  table: string;
  column?: string;
}

interface IndexedSchemaDiff {
  change: SchemaDiff;
  index: number;
}

function getNameSimilarity(a: string, b: string): number {
  const left = a.toLowerCase();
  const right = b.toLowerCase();

  if (left === right) return 1;
  if (left.length < 2 || right.length < 2) return 0;

  const leftBigrams = new Map<string, number>();
  for (let i = 0; i < left.length - 1; i++) {
    const gram = left.slice(i, i + 2);
    leftBigrams.set(gram, (leftBigrams.get(gram) ?? 0) + 1);
  }

  let intersection = 0;
  for (let i = 0; i < right.length - 1; i++) {
    const gram = right.slice(i, i + 2);
    const count = leftBigrams.get(gram) ?? 0;
    if (count > 0) {
      intersection++;
      leftBigrams.set(gram, count - 1);
    }
  }

  return (2 * intersection) / ((left.length - 1) + (right.length - 1));
}

function pairRenameCandidates(
  drops: IndexedSchemaDiff[],
  adds: IndexedSchemaDiff[],
  pairType: "table" | "column"
): AmbiguousChange[] {
  if (drops.length === 0 || adds.length === 0) return [];

  const candidates: Array<{ drop: IndexedSchemaDiff; add: IndexedSchemaDiff; score: number }> = [];
  for (const drop of drops) {
    for (const add of adds) {
      const dropName = pairType === "table" ? drop.change.table : drop.change.column;
      const addName = pairType === "table" ? add.change.table : add.change.column;
      candidates.push({
        drop,
        add,
        score: getNameSimilarity(dropName ?? "", addName ?? ""),
      });
    }
  }

  candidates.sort((a, b) => b.score - a.score);

  const usedDrops = new Set<number>();
  const usedAdds = new Set<number>();
  const pairs: AmbiguousChange[] = [];

  for (const candidate of candidates) {
    if (usedDrops.has(candidate.drop.index) || usedAdds.has(candidate.add.index)) {
      continue;
    }

		usedDrops.add(candidate.drop.index);
		usedAdds.add(candidate.add.index);

    pairs.push({
      type: pairType,
      table: (candidate.add.change.table ?? candidate.drop.change.table) ?? "",
      column: candidate.add.change.column,
      dropIndex: candidate.drop.index,
      addIndex: candidate.add.index,
    });

    if (pairs.length === Math.min(drops.length, adds.length)) {
      break;
    }
  }

  return pairs;
}

/**
 * Detect ambiguous changes (drop + add pairs that might be renames).
 * This is client-side since the API just returns raw changes.
 */
function detectAmbiguousChanges(changes: SchemaDiff[]): AmbiguousChange[] {
  const ambiguous: AmbiguousChange[] = [];

  // Find drop_table + add_table pairs
  const dropTables = changes
    .map((c, i) => ({ change: c, index: i }))
    .filter((c) => c.change.type === "drop_table");
  const addTables = changes
    .map((c, i) => ({ change: c, index: i }))
    .filter((c) => c.change.type === "add_table");

  ambiguous.push(...pairRenameCandidates(dropTables, addTables, "table"));

  // Find drop_column + add_column pairs within the same table
  const dropColumns = changes
    .map((c, i) => ({ change: c, index: i }))
    .filter((c) => c.change.type === "drop_column");
  const addColumns = changes
    .map((c, i) => ({ change: c, index: i }))
    .filter((c) => c.change.type === "add_column");

  const dropsByTable = new Map<string, IndexedSchemaDiff[]>();
  const addsByTable = new Map<string, IndexedSchemaDiff[]>();

  for (const drop of dropColumns) {
    const table = drop.change.table ?? "";
    const existing = dropsByTable.get(table) ?? [];
    existing.push(drop);
    dropsByTable.set(table, existing);
  }

  for (const add of addColumns) {
    const table = add.change.table ?? "";
    const existing = addsByTable.get(table) ?? [];
    existing.push(add);
    addsByTable.set(table, existing);
  }

  for (const [table, drops] of dropsByTable.entries()) {
    const adds = addsByTable.get(table) ?? [];
    ambiguous.push(...pairRenameCandidates(drops, adds, "column"));
  }

  return ambiguous;
}

/**
 * Resolve ambiguous changes by prompting the user.
 * Returns Merge[] for the API.
 */
async function resolveAmbiguousChanges(
  changes: SchemaDiff[],
  ambiguous: AmbiguousChange[]
): Promise<Merge[]> {
  const merges: Merge[] = [];

  if (ambiguous.length === 0) {
    return merges;
  }

  console.log("\nAmbiguous changes detected:\n");

  for (const change of ambiguous) {
    const dropChange = changes[change.dropIndex];
    const addChange = changes[change.addIndex];

    if (change.type === "table") {
      const isRename = await confirm(
        `  Table '${dropChange.table}' was removed and '${addChange.table}' was added.\n  Is this a rename?`
      );
      if (isRename) {
        merges.push({ old: change.dropIndex, new: change.addIndex });
      }
    } else {
      const isRename = await confirm(
        `  Column '${change.table}.${dropChange.column}' was removed and '${change.table}.${addChange.column}' was added.\n  Is this a rename?`
      );
      if (isRename) {
        merges.push({ old: change.dropIndex, new: change.addIndex });
      }
    }
  }

  console.log("");
  return merges;
}

function getUnmergedDestructiveChanges(changes: SchemaDiff[], merges?: Merge[]): DestructiveChange[] {
  const mergedDropIndexes = new Set((merges ?? []).map((m) => m.old));
  const destructive: DestructiveChange[] = [];

  for (let i = 0; i < changes.length; i++) {
    const change = changes[i];
    if ((change.type === "drop_table" || change.type === "drop_column") && !mergedDropIndexes.has(i)) {
      destructive.push({
        type: change.type,
        table: change.table ?? "",
        column: change.column,
      });
    }
  }

  return destructive;
}

/**
 * Print changes in a readable format.
 */
function printChanges(changes: SchemaDiff[]): void {
  if (!changes || changes.length === 0) {
    return;
  }

  console.log("\nChanges:");
  for (const change of changes) {
    const prefix =
      change.type.startsWith("add") ? "+" :
      change.type.startsWith("drop") ? "-" :
      change.type.startsWith("rename") ? ">" : "~";

    let desc = change.table || "";
    if (change.column) {
      desc += `.${change.column}`;
    }

    console.log(`  ${prefix} ${desc} (${change.type})`);
  }
}

/**
 * Push a single schema, handling ambiguous changes.
 */
async function pushSingleSchema(api: ApiClient, schema: SchemaDefinition): Promise<void> {
  console.log(`\nPushing schema "${schema.name}"...`);

  // Check if template exists
  const exists = await api.templateExists(schema.name);

  if (!exists) {
    // New template - just create it
    const result = await api.pushSchema(schema);
    console.log(`Created template "${schema.name}" (v${result.currentVersion})`);
    return;
  }

  // Existing template - check for changes first
  let diff;
  try {
    diff = await api.diffSchema(schema.name, schema);
  } catch (err) {
    // API returns 400 NO_CHANGES when schema is identical
    if (err instanceof ApiError && err.code === "NO_CHANGES") {
      console.log("No changes");
      return;
    }
    throw err;
  }

  if (!diff.changes || diff.changes.length === 0) {
    console.log("No changes");
    return;
  }

  // Show what we detected
  printChanges(diff.changes);

  // Detect and resolve ambiguous changes (client-side)
  const ambiguous = detectAmbiguousChanges(diff.changes);
  let merges: Merge[] | undefined;
  if (ambiguous.length > 0) {
    merges = await resolveAmbiguousChanges(diff.changes, ambiguous);
    if (merges.length === 0) {
      merges = undefined;
    }
  }

  const destructiveChanges = getUnmergedDestructiveChanges(diff.changes, merges);
  if (destructiveChanges.length > 0) {
    console.log("\nWarning: This migration will delete existing data:");
    for (const change of destructiveChanges) {
      if (change.type === "drop_table") {
        console.log(`  - Drop table: ${change.table}`);
      } else {
        console.log(`  - Drop column: ${change.table}.${change.column}`);
      }
    }

    const confirmed = await confirm("Continue with this destructive migration?");
    if (!confirmed) {
      console.log("Aborted.");
      process.exit(0);
    }
    console.log("");
  }

  // Apply the migration
  const result = await api.migrateTemplate(schema.name, schema, merges);
  console.log(`Template "${schema.name}" updated to v${result.currentVersion}`);
  console.log(`  Status: ${result.status}`);
}

// =============================================================================
// Pull Command Helpers
// =============================================================================

/**
 * Internal table format for code generation (columns as array with primaryKey flag).
 */
interface GeneratorTable {
  name: string;
  columns: GeneratorColumn[];
  indexes: { name: string; columns: string[]; unique?: boolean }[];
  ftsColumns: string[] | null;
}

interface GeneratorColumn extends Omit<ColumnDefinition, "references" | "onDelete" | "onUpdate"> {
  primaryKey: boolean;
  references: {
    table: string;
    column: string;
    onDelete?: ForeignKeyAction;
    onUpdate?: ForeignKeyAction;
  } | null;
}

/**
 * Convert API table format to internal generator format.
 */
function convertFromApiFormat(tables: TemplateResponse["schema"]["tables"]): GeneratorTable[] {
  return tables.map((table) => {
    const columns: GeneratorColumn[] = Object.entries(table.columns).map(([name, col]) => {
      const colDef: GeneratorColumn = {
        name,
        type: col.type,
        primaryKey: table.pk?.includes(name) ?? false,
        notNull: col.notNull,
        unique: col.unique,
        default: col.default,
        collate: col.collate as Collation | undefined,
        check: col.check,
        generated: col.generated,
        references: col.references
          ? {
              table: col.references.split(".")[0],
              column: col.references.split(".")[1],
              onDelete: col.onDelete as ForeignKeyAction | undefined,
              onUpdate: col.onUpdate as ForeignKeyAction | undefined,
            }
          : null,
      };
      return colDef;
    });

    return {
      name: table.name,
      columns,
      indexes: (table.indexes ?? []).map((idx) => ({
        name: idx.name,
        columns: idx.columns,
        unique: idx.unique,
      })),
      ftsColumns: table.ftsColumns ?? null,
    };
  });
}

/**
 * Generate TypeScript schema code from a template response.
 */
function generateSchemaCode(name: string, tables: GeneratorTable[]): string {
  const lines: string[] = [
    `import { defineSchema, defineTable, c } from "@atomicbase/template";`,
    ``,
    `export default defineSchema("${name}", {`,
  ];

  for (let i = 0; i < tables.length; i++) {
    const table = tables[i];
    const isLast = i === tables.length - 1;

    lines.push(`  ${table.name}: defineTable({`);

    for (const col of table.columns) {
      lines.push(`    ${col.name}: ${generateColumnCode(col)},`);
    }

    let tableSuffix = "})";

    // Add index chains
    for (const idx of table.indexes) {
      const method = idx.unique ? "uniqueIndex" : "index";
      tableSuffix += `.${method}("${idx.name}", ${JSON.stringify(idx.columns)})`;
    }

    // Add FTS chain
    if (table.ftsColumns && table.ftsColumns.length > 0) {
      tableSuffix += `.fts(${JSON.stringify(table.ftsColumns)})`;
    }

    tableSuffix += isLast ? "," : ",";
    lines.push(`  ${tableSuffix}`);

    if (!isLast) {
      lines.push(``);
    }
  }

  lines.push(`});`);
  lines.push(``);

  return lines.join("\n");
}

function generateColumnCode(col: GeneratorColumn): string {
  let code = `c.${col.type.toLowerCase()}()`;

  if (col.primaryKey) {
    code += ".primaryKey()";
  }
  if (col.notNull) {
    code += ".notNull()";
  }
  if (col.unique) {
    code += ".unique()";
  }
  if (col.collate) {
    code += `.collate("${col.collate}")`;
  }
  if (col.default !== undefined && col.default !== null) {
    const val = typeof col.default === "string"
      ? `"${col.default}"`
      : col.default;
    code += `.default(${val})`;
  }
  if (col.check) {
    code += `.check("${col.check.replace(/"/g, '\\"')}")`;
  }
  if (col.generated) {
    const opts = col.generated.stored ? ", { stored: true }" : "";
    code += `.generatedAs("${col.generated.expr.replace(/"/g, '\\"')}"${opts})`;
  }
  if (col.references) {
    const ref = `"${col.references.table}.${col.references.column}"`;
    if (col.references.onDelete || col.references.onUpdate) {
      const opts: string[] = [];
      if (col.references.onDelete) {
        opts.push(`onDelete: "${col.references.onDelete}"`);
      }
      if (col.references.onUpdate) {
        opts.push(`onUpdate: "${col.references.onUpdate}"`);
      }
      code += `.references(${ref}, { ${opts.join(", ")} })`;
    } else {
      code += `.references(${ref})`;
    }
  }

  return code;
}

interface PullOperation {
  type: "add" | "update";
  name: string;
  version: number;
  tableCount: number;
  code: string;
  outputPath: string;
}

// =============================================================================
// Command Implementations
// =============================================================================

/**
 * List all templates.
 */
async function listTemplates(): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  try {
    const templates = await api.listTemplates();

    if (templates.length === 0) {
      console.log("No templates found.");
      console.log("\nCreate one with: atomicbase templates push");
      return;
    }

    console.log("Templates:\n");
    console.log("  NAME                 VERSION    CREATED                  UPDATED");
    console.log("  " + "-".repeat(75));

    for (const template of templates) {
      const name = template.name.padEnd(20);
      const version = String(template.currentVersion).padEnd(10);
      const created = formatDate(template.createdAt).padEnd(24);
      const updated = formatDate(template.updatedAt);
      console.log(`  ${name} ${version} ${created} ${updated}`);
    }

    console.log(`\n  Total: ${templates.length} template(s)`);
  } catch (err) {
    console.error("Failed to list templates:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

/**
 * Get template details.
 */
async function getTemplate(name: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  try {
    const template = await api.getTemplate(name);

    console.log(`Template: ${template.name}\n`);
    console.log(`  ID:              ${template.id}`);
    console.log(`  Current Version: ${template.currentVersion}`);
    console.log(`  Created:         ${formatDate(template.createdAt)}`);
    console.log(`  Updated:         ${formatDate(template.updatedAt)}`);
    console.log(`  Tables:          ${template.schema.tables.length}`);

    if (template.schema.tables.length > 0) {
      console.log("\n  Schema:");
      for (const table of template.schema.tables) {
        const columnCount = Array.isArray(table.columns)
          ? table.columns.length
          : Object.keys(table.columns).length;
        console.log(`    - ${table.name} (${columnCount} columns)`);
      }
    }
  } catch (err) {
    console.error("Failed to get template:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

/**
 * Delete a template.
 */
async function deleteTemplate(name: string, force: boolean): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  if (!force) {
    // Verify template exists first
    try {
      await api.getTemplate(name);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        console.error(`Template "${name}" not found.`);
        process.exit(1);
      }
      throw err;
    }

    const confirmed = await confirm(
      `Are you sure you want to delete template "${name}"? This action cannot be undone.`
    );
    if (!confirmed) {
      console.log("Aborted.");
      process.exit(0);
    }
  }

  console.log(`Deleting template "${name}"...`);

  try {
    await api.deleteTemplate(name);
    console.log(`Deleted template "${name}"`);
  } catch (err) {
    console.error("Failed to delete template:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

/**
 * Show version history for a template.
 */
async function showHistory(name: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  try {
    const history = await api.getTemplateHistory(name);

    if (history.length === 0) {
      console.log(`No history found for template "${name}".`);
      return;
    }

    console.log(`Version history for template "${name}":\n`);
    console.log("  VERSION    CHECKSUM                           CREATED");
    console.log("  " + "-".repeat(70));

    for (const version of history) {
      const v = String(version.version).padEnd(10);
      const checksum = version.checksum.substring(0, 32).padEnd(34);
      const created = formatDate(version.createdAt);
      console.log(`  ${v} ${checksum} ${created}`);
    }

    console.log(`\n  Total: ${history.length} version(s)`);
    console.log(`\n  To rollback: atomicbase templates rollback ${name} <version>`);
  } catch (err) {
    console.error("Failed to get history:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

/**
 * Rollback a template to a previous version.
 */
async function rollbackTemplate(name: string, version: string, force: boolean): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  const targetVersion = parseInt(version, 10);
  if (isNaN(targetVersion) || targetVersion < 1) {
    console.error("Version must be a positive integer.");
    process.exit(1);
  }

  // Verify template and version exist
  let template;
  let history;
  try {
    template = await api.getTemplate(name);
    history = await api.getTemplateHistory(name);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      console.error(`Template "${name}" not found.`);
      process.exit(1);
    }
    throw err;
  }

  if (template.currentVersion === targetVersion) {
    console.log(`Template "${name}" is already at version ${targetVersion}.`);
    return;
  }

  const versionExists = history.some(v => v.version === targetVersion);
  if (!versionExists) {
    console.error(`Version ${targetVersion} not found in history.`);
    console.error("\nUse 'atomicbase templates history " + name + "' to see available versions.");
    process.exit(1);
  }

  if (!force) {
    const confirmed = await confirm(
      `Rollback template "${name}" from v${template.currentVersion} to v${targetVersion}? This will migrate all tenants.`
    );
    if (!confirmed) {
      console.log("Aborted.");
      process.exit(0);
    }
  }

  console.log(`Rolling back template "${name}" to version ${targetVersion}...`);

  try {
    const result = await api.rollbackTemplate(name, targetVersion);
    console.log(
      `Rollback applied: schema from v${targetVersion} is now published as new version v${result.currentVersion}`
    );
    console.log(`Template "${name}" updated to v${result.currentVersion}`);
    console.log(`  Status: ${result.status}`);
  } catch (err) {
    if (err instanceof ApiError && err.code === "NO_CHANGES") {
      console.log("No schema changes needed for this rollback.");
      return;
    }
    console.error("Failed to rollback:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

/**
 * Push schema(s) to the server.
 * If templateName is provided, pushes only that template.
 * Otherwise, pushes all templates.
 */
async function pushTemplates(templateName?: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  // Load schema(s)
  let schemas: SchemaDefinition[];
  try {
    const allSchemas = await loadAllSchemas(config.schemas);

    if (templateName) {
      // Push specific template by name
      const matched = allSchemas.filter((s) => s.name === templateName);

      if (matched.length === 0) {
        console.error(`Template "${templateName}" not found in local schemas.`);
        if (allSchemas.length > 0) {
          console.error(`\nAvailable templates:`);
          for (const s of allSchemas) {
            console.error(`  - ${s.name}`);
          }
        }
        process.exit(1);
      }

      schemas = matched;
    } else {
      // Push all schemas
      schemas = allSchemas;
    }
  } catch (err) {
    if (err instanceof Error) {
      console.error(`\n${err.message}`);
    } else {
      console.error(`\nFailed to load schemas:`, err);
    }
    process.exit(1);
  }

  if (schemas.length === 0) {
    console.log(`No schema files found in ${config.schemas}/`);
    console.log("Create a schema with: atomicbase init");
    return;
  }

  // Check for duplicate schema names
  const nameCount = new Map<string, number>();
  for (const schema of schemas) {
    nameCount.set(schema.name, (nameCount.get(schema.name) || 0) + 1);
  }
  const duplicates = [...nameCount.entries()].filter(([_, count]) => count > 1);
  if (duplicates.length > 0) {
    console.error("Error: Multiple schemas have the same name:\n");
    for (const [name, count] of duplicates) {
      console.error(`  "${name}" is defined ${count} times`);
    }
    console.error("\nEach schema must have a unique name. Check your schema files.");
    process.exit(1);
  }

  // Push each schema
  for (const schema of schemas) {
    try {
      await pushSingleSchema(api, schema);
    } catch (err) {
      console.error(`\nFailed to push "${schema.name}":`, err);
      process.exit(1);
    }
  }

  console.log("\nDone!");
}

/**
 * Pull schemas from the server.
 * If templateName is provided, pulls only that template.
 * Otherwise, pulls all templates.
 */
async function pullTemplates(templateName: string | undefined, options: { yes?: boolean }): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  try {
    // Load local templates
    let localSchemas: SchemaDefinition[] = [];
    try {
      localSchemas = await loadAllSchemas(config.schemas);
    } catch {
      // No local schemas or schemas directory doesn't exist - that's fine
    }
    const localTemplateNames = new Set(localSchemas.map((s) => s.name));

    // Determine which templates to fetch
    let templatesToFetch: { name: string }[];

    if (templateName) {
      // Pull specific template
      console.log(`Fetching template "${templateName}"...`);
      try {
        await api.getTemplate(templateName);
        templatesToFetch = [{ name: templateName }];
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          console.error(`Template "${templateName}" not found on server.`);
          if (err.hint) console.error(`\nHint: ${err.hint}`);
          process.exit(1);
        }
        throw err;
      }
    } else {
      // Pull all templates
      console.log("Fetching templates...");
      const cloudTemplates = await api.listTemplates();

      if (cloudTemplates.length === 0) {
        console.log("No templates found on server.");
        return;
      }
      templatesToFetch = cloudTemplates;
    }

    // Determine operations (add vs update vs skip)
    const operations: PullOperation[] = [];

    for (const templateInfo of templatesToFetch) {
      const template = await api.getTemplate(templateInfo.name);
      const code = generateSchemaCode(template.name, convertFromApiFormat(template.schema.tables));
      const outputPath = resolve(config.schemas, `${template.name}.schema.ts`);

      if (!localTemplateNames.has(template.name)) {
        // Template exists in cloud but not locally - add
        operations.push({
          type: "add",
          name: template.name,
          version: template.currentVersion,
          tableCount: template.schema.tables.length,
          code,
          outputPath,
        });
      } else {
        // Template exists both locally and in cloud - check for diff
        let existingCode = "";
        if (existsSync(outputPath)) {
          existingCode = readFileSync(outputPath, "utf-8");
        }

        if (existingCode !== code) {
          // Different content - update
          operations.push({
            type: "update",
            name: template.name,
            version: template.currentVersion,
            tableCount: template.schema.tables.length,
            code,
            outputPath,
          });
        }
        // If content is the same, skip (do nothing)
      }
    }

    if (operations.length === 0) {
      if (templateName) {
        console.log(`Local schema for "${templateName}" is up to date.`);
      } else {
        console.log("All local schemas are up to date.");
      }
      return;
    }

    // Show operations
    const adds = operations.filter((op) => op.type === "add");
    const updates = operations.filter((op) => op.type === "update");

    console.log("");

    if (adds.length > 0) {
      console.log(`Templates to add (${adds.length}):`);
      for (const op of adds) {
        console.log(`  + ${op.name} (v${op.version}, ${op.tableCount} tables)`);
      }
    }

    if (updates.length > 0) {
      if (adds.length > 0) console.log("");
      console.log(`Templates to update (${updates.length}):`);
      for (const op of updates) {
        console.log(`  ~ ${op.name} (v${op.version}, ${op.tableCount} tables)`);
      }
    }

    // Confirm unless --yes flag is set
    if (!options.yes) {
      console.log("");
      const confirmed = await confirm("Apply these changes?");
      if (!confirmed) {
        console.log("Aborted.");
        return;
      }
    }

    // Ensure schemas directory exists
    const schemasDir = config.schemas;
    if (!existsSync(schemasDir)) {
      mkdirSync(schemasDir, { recursive: true });
    }

    // Apply operations
    console.log("");
    for (const op of operations) {
      const outputDir = dirname(op.outputPath);
      if (!existsSync(outputDir)) {
        mkdirSync(outputDir, { recursive: true });
      }

      writeFileSync(op.outputPath, op.code);
      const action = op.type === "add" ? "Added" : "Updated";
      console.log(`  ${action} ${op.name}.schema.ts`);
    }

    console.log(`\nPulled ${operations.length} schema(s) to ${schemasDir}`);
  } catch (err) {
    console.error("Failed to pull schemas:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

/**
 * Preview changes without applying.
 */
async function diffTemplates(file?: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  // Load schema(s)
  let schemas: SchemaDefinition[];
  try {
    schemas = file
      ? [await loadSchema(file)]
      : await loadAllSchemas(config.schemas);
  } catch (err) {
    if (err instanceof Error) {
      console.error(`\n${err.message}`);
    } else {
      console.error(`\nFailed to load schemas:`, err);
    }
    process.exit(1);
  }

  if (schemas.length === 0) {
    console.log(`No schema files found in ${config.schemas}/`);
    return;
  }

  // Diff each schema
  for (const schema of schemas) {
    console.log(`Diffing schema "${schema.name}"...`);
    console.log("");

    try {
      const result = await api.diffSchema(schema.name, schema);

      if (result.changes.length === 0) {
        console.log("  No changes");
      } else {
        console.log("Changes:");
        for (const change of result.changes) {
          const prefix = change.type.startsWith("add") ? "+" :
                        change.type.startsWith("drop") ? "-" : "~";
          console.log(`  ${prefix} ${change.table}${change.column ? `.${change.column}` : ""} (${change.type})`);
        }
      }

      console.log("");
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        // API returns 400 NO_CHANGES when schema is identical
        if (err.code === "NO_CHANGES") {
          console.log("  No changes");
          console.log("");
          continue;
        }
        // Template might not exist yet - that's fine, show what would be created
        if (err.status === 404) {
          console.log("  Template does not exist yet. Push will create:");
          console.log(`  + ${schema.tables.length} table(s)`);
          for (const table of schema.tables) {
            const columnCount = Object.keys(table.columns).length;
            console.log(`    + ${table.name} (${columnCount} columns)`);
          }
          console.log("");
          continue;
        }
      }
      console.error(`Failed to diff "${schema.name}":`, err);
    }
  }
}

// =============================================================================
// Command Registration
// =============================================================================

export const templatesCommand = new Command("templates")
  .description("Manage schema templates");

// templates list
templatesCommand
  .command("list")
  .alias("ls")
  .description("List all templates")
  .action(listTemplates);

// templates get <name>
templatesCommand
  .command("get <name>")
  .description("Get template details")
  .action(getTemplate);

// templates push [name]
templatesCommand
  .command("push [name]")
  .description("Push schema(s) to the server")
  .action(pushTemplates);

// templates pull [name]
templatesCommand
  .command("pull [name]")
  .description("Pull schema(s) from the server")
  .option("-y, --yes", "Skip confirmation prompt")
  .action((name: string | undefined, options: { yes?: boolean }) => {
    pullTemplates(name, options);
  });

// templates diff [file]
templatesCommand
  .command("diff [file]")
  .description("Preview changes without applying")
  .action(diffTemplates);

// templates delete <name>
templatesCommand
  .command("delete <name>")
  .alias("rm")
  .description("Delete a template (only if no tenants use it)")
  .option("-f, --force", "Skip confirmation prompt")
  .action((name: string, options: { force?: boolean }) => {
    deleteTemplate(name, options.force ?? false);
  });

// templates history <name>
templatesCommand
  .command("history <name>")
  .description("View version history for a template")
  .action(showHistory);

// templates rollback <name> <version>
templatesCommand
  .command("rollback <name> <version>")
  .description("Rollback to a previous schema version")
  .option("-f, --force", "Skip confirmation prompt")
  .action((name: string, version: string, options: { force?: boolean }) => {
    rollbackTemplate(name, version, options.force ?? false);
  });
