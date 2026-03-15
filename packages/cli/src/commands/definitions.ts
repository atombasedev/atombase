import { Command } from "commander";
import { createInterface } from "readline";
import { loadConfig } from "../config.js";
import { loadSchema, loadAllSchemas } from "../schema/parser.js";
import {
  ApiClient,
  ApiError,
  type SchemaDiff,
  type Merge,
  type DefinitionResponse,
} from "../api.js";
import type { DefinitionDefinition, SchemaDefinition, TableDefinition } from "@atomicbase/definitions";

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

interface AmbiguousChange {
  type: "table" | "column";
  table: string;
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
    if (usedDrops.has(candidate.drop.index) || usedAdds.has(candidate.add.index)) continue;
    usedDrops.add(candidate.drop.index);
    usedAdds.add(candidate.add.index);
    pairs.push({
      type: pairType,
      table: candidate.add.change.table ?? candidate.drop.change.table ?? "",
      dropIndex: candidate.drop.index,
      addIndex: candidate.add.index,
    });
  }

  return pairs;
}

function detectAmbiguousChanges(changes: SchemaDiff[]): AmbiguousChange[] {
  const indexed = changes.map((change, index) => ({ change, index }));
  const ambiguous: AmbiguousChange[] = [];
  ambiguous.push(
    ...pairRenameCandidates(
      indexed.filter((item) => item.change.type === "drop_table"),
      indexed.filter((item) => item.change.type === "add_table"),
      "table"
    )
  );

  const dropColumns = indexed.filter((item) => item.change.type === "drop_column");
  const addColumns = indexed.filter((item) => item.change.type === "add_column");
  const dropsByTable = new Map<string, IndexedSchemaDiff[]>();
  const addsByTable = new Map<string, IndexedSchemaDiff[]>();

  for (const drop of dropColumns) {
    const existing = dropsByTable.get(drop.change.table ?? "") ?? [];
    existing.push(drop);
    dropsByTable.set(drop.change.table ?? "", existing);
  }
  for (const add of addColumns) {
    const existing = addsByTable.get(add.change.table ?? "") ?? [];
    existing.push(add);
    addsByTable.set(add.change.table ?? "", existing);
  }

  for (const [table, drops] of dropsByTable.entries()) {
    const adds = addsByTable.get(table) ?? [];
    ambiguous.push(...pairRenameCandidates(drops, adds, "column"));
  }

  return ambiguous;
}

async function resolveAmbiguousChanges(changes: SchemaDiff[], ambiguous: AmbiguousChange[]): Promise<Merge[]> {
  const merges: Merge[] = [];
  if (ambiguous.length === 0) return merges;

  console.log("\nAmbiguous changes detected:\n");
  for (const candidate of ambiguous) {
    const drop = changes[candidate.dropIndex];
    const add = changes[candidate.addIndex];
    const question = candidate.type === "table"
      ? `  Table '${drop.table}' was removed and '${add.table}' was added.\n  Is this a rename?`
      : `  Column '${candidate.table}.${drop.column}' was removed and '${candidate.table}.${add.column}' was added.\n  Is this a rename?`;
    if (await confirm(question)) {
      merges.push({ old: candidate.dropIndex, new: candidate.addIndex });
    }
  }

  console.log("");
  return merges;
}

function getUnmergedDestructiveChanges(changes: SchemaDiff[], merges?: Merge[]): DestructiveChange[] {
  const merged = new Set((merges ?? []).map((merge) => merge.old));
  return changes
    .map((change, index) => ({ change, index }))
    .filter((item) => (item.change.type === "drop_table" || item.change.type === "drop_column") && !merged.has(item.index))
    .map((item) => ({
      type: item.change.type as "drop_table" | "drop_column",
      table: item.change.table ?? "",
      column: item.change.column,
    }));
}

function printChanges(changes: SchemaDiff[]): void {
  if (changes.length === 0) return;
  console.log("\nChanges:");
  for (const change of changes) {
    const prefix = change.type.startsWith("add")
      ? "+"
      : change.type.startsWith("drop")
        ? "-"
        : "~";
    console.log(`  ${prefix} ${change.table ?? ""}${change.column ? `.${change.column}` : ""} (${change.type})`);
  }
}

function tableMap(schema: SchemaDefinition): Map<string, TableDefinition> {
  return new Map(schema.tables.map((table) => [table.name, table]));
}

function indexesMap(table: TableDefinition): Map<string, { name: string; columns: string[]; unique?: boolean }> {
  return new Map((table.indexes ?? []).map((idx) => [idx.name, idx]));
}

function ftsSet(table: TableDefinition): Set<string> {
  return new Set(table.ftsColumns ?? []);
}

function equalJSON(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

function columnModified(oldCol: TableDefinition["columns"][string], newCol: TableDefinition["columns"][string]): boolean {
  return oldCol.type !== newCol.type ||
    oldCol.notNull !== newCol.notNull ||
    oldCol.unique !== newCol.unique ||
    oldCol.collate !== newCol.collate ||
    oldCol.check !== newCol.check ||
    oldCol.references !== newCol.references ||
    oldCol.onDelete !== newCol.onDelete ||
    oldCol.onUpdate !== newCol.onUpdate ||
    !equalJSON(oldCol.default, newCol.default) ||
    !equalJSON(oldCol.generated, newCol.generated);
}

function pkTypeChanged(oldTable: TableDefinition, newTable: TableDefinition): boolean {
  if (oldTable.pk.length !== newTable.pk.length) return true;
  for (let i = 0; i < oldTable.pk.length; i++) {
    if (oldTable.pk[i] !== newTable.pk[i]) return true;
    const oldCol = oldTable.columns[oldTable.pk[i]];
    const newCol = newTable.columns[newTable.pk[i]];
    if (oldCol && newCol && oldCol.type !== newCol.type) return true;
  }
  return false;
}

function diffSchemas(oldSchema: SchemaDefinition, newSchema: SchemaDefinition): SchemaDiff[] {
  const changes: SchemaDiff[] = [];
  const oldTables = tableMap(oldSchema);
  const newTables = tableMap(newSchema);

  for (const name of oldTables.keys()) {
    if (!newTables.has(name)) {
      changes.push({ type: "drop_table", table: name });
    }
  }

  for (const [name, newTable] of newTables.entries()) {
    const oldTable = oldTables.get(name);
    if (!oldTable) {
      changes.push({ type: "add_table", table: name });
      continue;
    }

    for (const colName of Object.keys(oldTable.columns)) {
      if (!newTable.columns[colName]) {
        changes.push({ type: "drop_column", table: name, column: colName });
      }
    }

    for (const [colName, newCol] of Object.entries(newTable.columns)) {
      const oldCol = oldTable.columns[colName];
      if (!oldCol) {
        changes.push({ type: "add_column", table: name, column: colName });
        continue;
      }
      if (columnModified(oldCol, newCol)) {
        changes.push({ type: "modify_column", table: name, column: colName });
      }
    }

    const oldIndexes = indexesMap(oldTable);
    const newIndexes = indexesMap(newTable);
    for (const indexName of oldIndexes.keys()) {
      if (!newIndexes.has(indexName)) {
        changes.push({ type: "drop_index", table: name, column: indexName });
      }
    }
    for (const indexName of newIndexes.keys()) {
      if (!oldIndexes.has(indexName)) {
        changes.push({ type: "add_index", table: name, column: indexName });
      }
    }

    const oldFTS = ftsSet(oldTable);
    const newFTS = ftsSet(newTable);
    if (oldFTS.size === 0 && newFTS.size > 0) {
      changes.push({ type: "add_fts", table: name });
    } else if (oldFTS.size > 0 && newFTS.size === 0) {
      changes.push({ type: "drop_fts", table: name });
    } else if (!equalJSON([...oldFTS].sort(), [...newFTS].sort())) {
      changes.push({ type: "drop_fts", table: name });
      changes.push({ type: "add_fts", table: name });
    }

    if (pkTypeChanged(oldTable, newTable)) {
      changes.push({ type: "change_pk_type", table: name });
    }
  }

  return changes;
}

async function pushSingleDefinition(api: ApiClient, definition: DefinitionDefinition): Promise<void> {
  const name = definition.name ?? "";
  console.log(`\nPushing definition "${name}"...`);

  const exists = await api.definitionExists(name);
  if (!exists) {
    const result = await api.createDefinition(definition);
    console.log(`Created definition "${result.name}" (v${result.currentVersion})`);
    return;
  }

  const current = await api.getDefinition(name);
  if (!current.schema) {
    throw new Error(`Definition "${name}" is missing schema data from the API`);
  }

  const changes = diffSchemas(current.schema, definition.schema);
  if (changes.length === 0) {
    console.log("No schema changes");
    return;
  }

  printChanges(changes);
  const ambiguous = detectAmbiguousChanges(changes);
  let merges: Merge[] | undefined;
  if (ambiguous.length > 0) {
    merges = await resolveAmbiguousChanges(changes, ambiguous);
    if (merges.length === 0) merges = undefined;
  }

  const destructiveChanges = getUnmergedDestructiveChanges(changes, merges);
  if (destructiveChanges.length > 0) {
    console.log("\nWarning: This migration will delete existing data:");
    for (const change of destructiveChanges) {
      if (change.type === "drop_table") {
        console.log(`  - Drop table: ${change.table}`);
      } else {
        console.log(`  - Drop column: ${change.table}.${change.column}`);
      }
    }

    if (!(await confirm("Continue with this destructive migration?"))) {
      console.log("Aborted.");
      process.exit(0);
    }
    console.log("");
  }

  const result = await api.pushDefinition(name, definition, merges);
  console.log(`Definition "${name}" updated to v${result.version}`);
}

async function listDefinitions(): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);
  try {
    const definitions = await api.listDefinitions();
    if (definitions.length === 0) {
      console.log("No definitions found.");
      console.log("\nCreate one with: atomicbase definitions push");
      return;
    }

    console.log("Definitions:\n");
    console.log("  NAME                 TYPE           VERSION    UPDATED");
    console.log("  " + "-".repeat(72));
    for (const definition of definitions) {
      console.log(
        `  ${definition.name.padEnd(20)} ${definition.type.padEnd(14)} ${String(definition.currentVersion).padEnd(10)} ${formatDate(definition.updatedAt)}`
      );
    }
    console.log(`\n  Total: ${definitions.length} definition(s)`);
  } catch (err) {
    console.error("Failed to list definitions:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

async function getDefinition(name: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);
  try {
    const definition = await api.getDefinition(name);
    console.log(`Definition: ${definition.name}\n`);
    console.log(`  ID:              ${definition.id}`);
    console.log(`  Type:            ${definition.type}`);
    console.log(`  Current Version: ${definition.currentVersion}`);
    console.log(`  Created:         ${formatDate(definition.createdAt)}`);
    console.log(`  Updated:         ${formatDate(definition.updatedAt)}`);
    if (definition.roles && definition.roles.length > 0) {
      console.log(`  Roles:           ${definition.roles.join(", ")}`);
    }
    if (definition.schema) {
      console.log(`  Tables:          ${definition.schema.tables.length}`);
    }
  } catch (err) {
    console.error("Failed to get definition:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

async function pushDefinitions(definitionName?: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  let definitions: DefinitionDefinition[];
  try {
    const allDefinitions = await loadAllSchemas(config.schemas);
    definitions = definitionName
      ? allDefinitions.filter((definition) => definition.name === definitionName)
      : allDefinitions;

    if (definitionName && definitions.length === 0) {
      console.error(`Definition "${definitionName}" not found in local files.`);
      process.exit(1);
    }
  } catch (err) {
    console.error(err instanceof Error ? `\n${err.message}` : `\nFailed to load definitions: ${err}`);
    process.exit(1);
  }

  if (definitions.length === 0) {
    console.log(`No definition files found in ${config.schemas}/`);
    console.log("Create one with: atomicbase init");
    return;
  }

  const names = new Map<string, number>();
  for (const definition of definitions) {
    const name = definition.name ?? "";
    names.set(name, (names.get(name) ?? 0) + 1);
  }
  const duplicates = [...names.entries()].filter(([, count]) => count > 1);
  if (duplicates.length > 0) {
    console.error("Error: Multiple local definitions have the same name:\n");
    for (const [name, count] of duplicates) {
      console.error(`  "${name}" is defined ${count} times`);
    }
    process.exit(1);
  }

  for (const definition of definitions) {
    try {
      await pushSingleDefinition(api, definition);
    } catch (err) {
      console.error(`\nFailed to push "${definition.name}":`, err instanceof ApiError ? err.format() : err);
      process.exit(1);
    }
  }

  console.log("\nDone!");
}

async function diffDefinitions(file?: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);

  let definitions: DefinitionDefinition[];
  try {
    definitions = file ? [await loadSchema(file)] : await loadAllSchemas(config.schemas);
  } catch (err) {
    console.error(err instanceof Error ? `\n${err.message}` : `\nFailed to load definitions: ${err}`);
    process.exit(1);
  }

  if (definitions.length === 0) {
    console.log(`No definition files found in ${config.schemas}/`);
    return;
  }

  for (const definition of definitions) {
    const name = definition.name ?? "";
    console.log(`Diffing definition "${name}"...\n`);
    try {
      const remote = await api.getDefinition(name);
      if (!remote.schema) {
        console.log("  Remote definition has no schema payload.\n");
        continue;
      }

      const changes = diffSchemas(remote.schema, definition.schema);
      if (changes.length === 0) {
        console.log("  No schema changes\n");
        continue;
      }
      printChanges(changes);
      console.log("");
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        console.log("  Definition does not exist yet. Push will create it.\n");
        continue;
      }
      console.error(`Failed to diff "${name}":`, err instanceof ApiError ? err.format() : err);
    }
  }
}

async function showHistory(name: string): Promise<void> {
  const config = await loadConfig();
  const api = new ApiClient(config);
  try {
    const history = await api.getDefinitionHistory(name);
    if (history.length === 0) {
      console.log(`No history found for definition "${name}".`);
      return;
    }

    console.log(`Version history for definition "${name}":\n`);
    console.log("  VERSION    CHECKSUM                           CREATED");
    console.log("  " + "-".repeat(70));
    for (const version of history) {
      console.log(`  ${String(version.version).padEnd(10)} ${version.checksum.substring(0, 32).padEnd(34)} ${formatDate(version.createdAt)}`);
    }
    console.log(`\n  Total: ${history.length} version(s)`);
  } catch (err) {
    console.error("Failed to get history:", err instanceof ApiError ? err.format() : err);
    process.exit(1);
  }
}

export const definitionsCommand = new Command("definitions")
  .description("Manage definitions");

definitionsCommand
  .command("list")
  .alias("ls")
  .description("List all definitions")
  .action(listDefinitions);

definitionsCommand
  .command("get <name>")
  .description("Get definition details")
  .action(getDefinition);

definitionsCommand
  .command("push [name]")
  .description("Push local definition files to the server")
  .action(pushDefinitions);

definitionsCommand
  .command("diff [file]")
  .description("Preview local schema changes against the remote definition")
  .action(diffDefinitions);

definitionsCommand
  .command("history <name>")
  .description("View definition version history")
  .action(showHistory);
