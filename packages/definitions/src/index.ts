// =============================================================================
// Schema Definition SDK
// =============================================================================
//
// TypeScript-first schema definitions for Atomicbase definitions.
//
// Example:
// ```typescript
// import { defineSchema, defineTable, c } from "@atomicbase/definitions";
//
// export default defineSchema("user-app", {
//   users: defineTable({
//     id: c.integer().primaryKey(),
//     email: c.text().notNull().unique(),
//     name: c.text().notNull(),
//   }),
// });
// ```

// =============================================================================
// Types - Match Go API types in platform/types.go
// =============================================================================

export type ColumnType = "INTEGER" | "TEXT" | "REAL" | "BLOB";

export type ForeignKeyAction =
  | "CASCADE"
  | "SET NULL"
  | "RESTRICT"
  | "NO ACTION";

export type Collation = "BINARY" | "NOCASE" | "RTRIM";

export interface SQLExpression {
  sql: string;
}

export interface ForeignKeyOptions {
  onDelete?: ForeignKeyAction;
  onUpdate?: ForeignKeyAction;
}

export interface GeneratedColumn {
  expr: string;
  stored?: boolean; // true=STORED, false/undefined=VIRTUAL
}

/**
 * Column definition matching Go API's Col type.
 */
export interface ColumnDefinition {
  name: string;
  type: ColumnType;
  notNull?: boolean;
  unique?: boolean;
  default?: string | number | SQLExpression | null;
  collate?: Collation;
  check?: string;
  generated?: GeneratedColumn;
  references?: string; // Foreign key reference in "table.column" format
  onDelete?: ForeignKeyAction;
  onUpdate?: ForeignKeyAction;
}

/**
 * Index definition matching Go API's Index type.
 */
export interface IndexDefinition {
  name: string;
  columns: string[];
  unique?: boolean;
}

/**
 * Table definition matching Go API's Table type.
 */
export interface TableDefinition {
  name: string;
  pk: string[]; // Primary key column name(s)
  columns: Record<string, ColumnDefinition>;
  indexes?: IndexDefinition[];
  ftsColumns?: string[];
}

/**
 * Schema definition matching Go API's Schema type.
 */
export interface SchemaDefinition {
  name?: string;
  tables: TableDefinition[];
}

export type DefinitionType = "global" | "user" | "organization";

export interface Condition {
  field?: string;
  op?: string;
  value?: unknown;
  and?: Condition[];
  or?: Condition[];
  not?: Condition;
}

export interface OperationPolicy {
  select?: Condition;
  insert?: Condition;
  update?: Condition;
  delete?: Condition;
}

export type AccessDefinition = Record<string, OperationPolicy>;

export type ManagementPermission =
  | boolean
  | string[]
  | { any: true };

export interface ManagementPolicy {
  invite?: ManagementPermission;
  assignRole?: ManagementPermission;
  removeMember?: ManagementPermission;
  updateOrg?: boolean;
  deleteOrg?: boolean;
  transferOwnership?: boolean;
}

export type ManagementDefinition = Record<string, ManagementPolicy>;

export interface GlobalDefinition {
  name?: string;
  type: "global";
  schema: SchemaDefinition;
  access: AccessDefinition;
}

export interface UserDefinition {
  name?: string;
  type: "user";
  schema: SchemaDefinition;
  access: AccessDefinition;
}

export interface OrganizationDefinition {
  name?: string;
  type: "organization";
  roles?: string[];
  maxMembers?: number;
  management?: ManagementDefinition;
  schema: SchemaDefinition;
  access: AccessDefinition;
}

export type DefinitionDefinition =
  | GlobalDefinition
  | UserDefinition
  | OrganizationDefinition;

type FieldReference = {
  __kind: "field_ref";
  path: string;
};

// =============================================================================
// Column Builder
// =============================================================================

/**
 * Builder class for defining column properties with chainable methods.
 */
export class ColumnBuilder {
  private _type: ColumnType;
  private _primaryKey = false;
  private _notNull = false;
  private _unique = false;
  private _default: string | number | SQLExpression | null = null;
  private _collate: Collation | undefined = undefined;
  private _check: string | undefined = undefined;
  private _generated: GeneratedColumn | undefined = undefined;
  private _references: string | undefined = undefined;
  private _onDelete: ForeignKeyAction | undefined = undefined;
  private _onUpdate: ForeignKeyAction | undefined = undefined;

  constructor(type: ColumnType) {
    this._type = type;
  }

  /**
   * Mark column as PRIMARY KEY.
   */
  primaryKey(): this {
    this._primaryKey = true;
    return this;
  }

  /**
   * Add NOT NULL constraint.
   */
  notNull(): this {
    this._notNull = true;
    return this;
  }

  /**
   * Add UNIQUE constraint.
   */
  unique(): this {
    this._unique = true;
    return this;
  }

  /**
   * Set default value.
   * @param value - Literal value or SQL expression (e.g., "CURRENT_TIMESTAMP")
   */
  default(value: string | number | SQLExpression): this {
    this._default = value;
    return this;
  }

  /**
   * Set collation for text comparison.
   * @param collation - BINARY (default), NOCASE (case-insensitive), or RTRIM
   */
  collate(collation: Collation): this {
    this._collate = collation;
    return this;
  }

  /**
   * Add CHECK constraint.
   * @param expr - SQL expression for validation (e.g., "age > 0")
   */
  check(expr: string): this {
    this._check = expr;
    return this;
  }

  /**
   * Define as a generated/computed column.
   * @param expr - SQL expression to compute value
   * @param options - { stored: true } for STORED, omit for VIRTUAL
   */
  generatedAs(expr: string, options?: { stored?: boolean }): this {
    this._generated = {
      expr,
      stored: options?.stored,
    };
    return this;
  }

  /**
   * Add foreign key reference.
   * @param ref - Reference in "table.column" format
   * @param options - Optional cascade options
   */
  references(ref: string, options?: ForeignKeyOptions): this {
    const parts = ref.split(".");
    if (parts.length !== 2 || !parts[0] || !parts[1]) {
      throw new Error(
        `Invalid reference format: "${ref}". Expected "table.column"`
      );
    }
    this._references = ref;
    this._onDelete = options?.onDelete;
    this._onUpdate = options?.onUpdate;
    return this;
  }

  /**
   * Check if this column is a primary key.
   * @internal
   */
  _isPrimaryKey(): boolean {
    return this._primaryKey;
  }

  /**
   * Build the column definition object.
   * @internal
   */
  _build(name: string): ColumnDefinition {
    const col: ColumnDefinition = {
      name,
      type: this._type,
    };

    // Only include optional fields if they have values
    if (this._notNull) col.notNull = true;
    if (this._unique) col.unique = true;
    if (this._default !== null) col.default = this._default;
    if (this._collate) col.collate = this._collate;
    if (this._check) col.check = this._check;
    if (this._generated) col.generated = this._generated;
    if (this._references) col.references = this._references;
    if (this._onDelete) col.onDelete = this._onDelete;
    if (this._onUpdate) col.onUpdate = this._onUpdate;

    return col;
  }
}

// =============================================================================
// Column Type Factories
// =============================================================================

/**
 * Column type builders.
 *
 * ```typescript
 * c.integer()  // INTEGER - whole numbers, booleans (0/1), timestamps
 * c.text()     // TEXT - strings, JSON, ISO dates
 * c.real()     // REAL - floating point numbers
 * c.blob()     // BLOB - binary data
 * ```
 */
export const c = {
  /**
   * INTEGER column - whole numbers, booleans (0/1), unix timestamps.
   */
  integer: () => new ColumnBuilder("INTEGER"),

  /**
   * TEXT column - strings, JSON, ISO dates.
   */
  text: () => new ColumnBuilder("TEXT"),

  /**
   * REAL column - floating point numbers.
   */
  real: () => new ColumnBuilder("REAL"),

  /**
   * BLOB column - binary data.
   */
  blob: () => new ColumnBuilder("BLOB"),
};

/**
 * Raw SQL expression for defaults.
 *
 * Example:
 * c.text().default(sql("CURRENT_TIMESTAMP"))
 */
export function sql(expression: string): SQLExpression {
  return { sql: expression };
}

// =============================================================================
// Table Builder
// =============================================================================

/**
 * Builder class for defining table properties.
 */
export class TableBuilder {
  private _columns: Record<string, ColumnBuilder>;
  private _indexes: IndexDefinition[] = [];
  private _ftsColumns: string[] | undefined = undefined;

  constructor(columns: Record<string, ColumnBuilder>) {
    this._columns = columns;
  }

  /**
   * Add an index on specified columns.
   * @param name - Index name
   * @param columns - Column names to index
   */
  index(name: string, columns: string[]): this {
    this._indexes.push({ name, columns });
    return this;
  }

  /**
   * Add a unique index on specified columns.
   * @param name - Index name
   * @param columns - Column names to index
   */
  uniqueIndex(name: string, columns: string[]): this {
    this._indexes.push({ name, columns, unique: true });
    return this;
  }

  /**
   * Enable FTS5 full-text search on specified columns.
   * Creates a virtual table: {tableName}_fts
   * @param columns - Column names to include in FTS
   */
  fts(columns: string[]): this {
    this._ftsColumns = columns;
    return this;
  }

  /**
   * Build the table definition object.
   * @internal
   */
  _build(name: string): TableDefinition {
    const columns: Record<string, ColumnDefinition> = {};
    const pk: string[] = [];

    for (const [colName, builder] of Object.entries(this._columns)) {
      columns[colName] = builder._build(colName);
      if (builder._isPrimaryKey()) {
        pk.push(colName);
      }
    }

    const table: TableDefinition = {
      name,
      pk,
      columns,
    };

    if (this._indexes.length > 0) table.indexes = this._indexes;
    if (this._ftsColumns) table.ftsColumns = this._ftsColumns;

    return table;
  }
}

// =============================================================================
// Define Functions
// =============================================================================

/**
 * Define a table with columns and optional indexes/FTS.
 *
 * ```typescript
 * const users = defineTable({
 *   id: c.integer().primaryKey(),
 *   email: c.text().notNull().unique(),
 *   name: c.text().notNull(),
 * }).index("idx_email", ["email"]);
 * ```
 */
export function defineTable(
  columns: Record<string, ColumnBuilder>
): TableBuilder {
  return new TableBuilder(columns);
}

/**
 * Define a schema template with multiple tables.
 *
 * ```typescript
 * export default defineSchema("user-app", {
 *   users: defineTable({ ... }),
 *   projects: defineTable({ ... }),
 * });
 * ```
 */
export function defineSchema(name: string, tables: Record<string, TableBuilder>): SchemaDefinition;
export function defineSchema(tables: Record<string, TableBuilder>): SchemaDefinition;
export function defineSchema(
  nameOrTables: string | Record<string, TableBuilder>,
  maybeTables?: Record<string, TableBuilder>
): SchemaDefinition {
  const name = typeof nameOrTables === "string" ? nameOrTables : undefined;
  const tables = typeof nameOrTables === "string" ? maybeTables ?? {} : nameOrTables;
  const tableDefinitions: TableDefinition[] = [];

  for (const [tableName, builder] of Object.entries(tables)) {
    tableDefinitions.push(builder._build(tableName));
  }

  return {
    name,
    tables: tableDefinitions,
  };
}

function field(path: string): FieldReference {
  return { __kind: "field_ref", path };
}

function isFieldReference(value: unknown): value is FieldReference {
  return typeof value === "object" && value !== null && (value as FieldReference).__kind === "field_ref";
}

function serializeValue(value: unknown): unknown {
  if (isFieldReference(value)) {
    return value.path;
  }
  if (Array.isArray(value)) {
    return value.map((item) => serializeValue(item));
  }
  return value;
}

function createScopeProxy(scope: string): Record<string, FieldReference> {
  return new Proxy(
    {},
    {
      get(_target, prop) {
        if (typeof prop !== "string") return undefined;
        return field(`${scope}.${prop}`);
      },
    }
  ) as Record<string, FieldReference>;
}

type PolicyContext = {
  auth: Record<string, FieldReference>;
  old: Record<string, FieldReference>;
  new: Record<string, FieldReference>;
};

type ManagementRoleContext = Record<string, { __kind: "role_ref"; role: string }> & {
  any(): { any: true };
};

export const r = {
  allow(): Condition {
    return {};
  },
  where(builder: (ctx: PolicyContext) => Condition): Condition {
    return builder({
      auth: createScopeProxy("auth"),
      old: createScopeProxy("old"),
      new: createScopeProxy("new"),
    });
  },
};

export function eq(left: unknown, right: unknown): Condition {
  if (isFieldReference(left)) {
    return { field: left.path, op: "eq", value: serializeValue(right) };
  }
  if (isFieldReference(right)) {
    return { field: right.path, op: "eq", value: serializeValue(left) };
  }
  throw new Error("eq requires at least one field reference");
}

export function inList(left: unknown, values: unknown[]): Condition {
  if (!isFieldReference(left)) {
    throw new Error("inList requires a field reference as the first argument");
  }
  return { field: left.path, op: "in", value: values.map((value) => serializeValue(value)) };
}

export function and(...conditions: Condition[]): Condition {
  return { and: conditions };
}

export function or(...conditions: Condition[]): Condition {
  return { or: conditions };
}

export function not(condition: Condition): Condition {
  return { not: condition };
}

export function definePolicy(policy: OperationPolicy): OperationPolicy {
  return policy;
}

export function defineAccess(access: AccessDefinition): AccessDefinition {
  return access;
}

export function defineManagement(builder: (role: ManagementRoleContext) => ManagementDefinition): ManagementDefinition {
  const proxy = new Proxy(
    {},
    {
      get(_target, prop) {
        if (prop === "any") {
          return () => ({ any: true as const });
        }
        if (typeof prop !== "string") return undefined;
        return { __kind: "role_ref", role: prop };
      },
    }
  ) as ManagementRoleContext;

  return normalizeManagement(builder(proxy));
}

function normalizeManagement(input: ManagementDefinition): ManagementDefinition {
  const out: ManagementDefinition = {};
  for (const [roleName, policy] of Object.entries(input)) {
    out[roleName] = {
      invite: normalizeManagementPermission(policy.invite),
      assignRole: normalizeManagementPermission(policy.assignRole),
      removeMember: normalizeManagementPermission(policy.removeMember),
      updateOrg: policy.updateOrg,
      deleteOrg: policy.deleteOrg,
      transferOwnership: policy.transferOwnership,
    };
  }
  return out;
}

function normalizeManagementPermission(permission: ManagementPermission | undefined): ManagementPermission | undefined {
  if (permission === undefined || typeof permission === "boolean") {
    return permission;
  }
  if (Array.isArray(permission)) {
    return permission.map((item) => normalizeRoleValue(item));
  }
  if (typeof permission === "object" && permission !== null && "any" in permission) {
    return { any: true };
  }
  return permission;
}

function normalizeRoleValue(value: unknown): string {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "object" && value !== null && "__kind" in value && (value as { __kind?: string }).__kind === "role_ref") {
    return (value as unknown as { role: string }).role;
  }
  throw new Error("management role lists must contain role references");
}

export function defineGlobal(input: Omit<GlobalDefinition, "type">): GlobalDefinition {
  return {
    ...input,
    type: "global",
  };
}

export function defineUser(input: Omit<UserDefinition, "type">): UserDefinition {
  return {
    ...input,
    type: "user",
  };
}

export function defineOrg(input: Omit<OrganizationDefinition, "type">): OrganizationDefinition {
  return {
    ...input,
    type: "organization",
  };
}
