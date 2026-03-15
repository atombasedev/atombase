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

export interface Condition<FieldPath extends string = string> {
  field?: FieldPath;
  op?: string;
  value?: unknown;
  and?: Condition<FieldPath>[];
  or?: Condition<FieldPath>[];
  not?: Condition<FieldPath>;
}

export interface OperationPolicy<FieldPath extends string = string> {
  select?: Condition<FieldPath>;
  insert?: Condition<FieldPath>;
  update?: Condition<FieldPath>;
  delete?: Condition<FieldPath>;
}

export type AccessDefinition<TableName extends string = string, FieldPath extends string = string> = Partial<Record<TableName, OperationPolicy<FieldPath>>>;

type RoleReference<RoleName extends string> = {
  __kind: "role_ref";
  role: RoleName;
};

export type ManagementPermission<RoleName extends string = string> =
  | boolean
  | RoleName
  | RoleName[]
  | RoleReference<RoleName>
  | RoleReference<RoleName>[]
  | { any: true };

export interface ManagementPolicy<RoleName extends string = string> {
  invite?: ManagementPermission<RoleName>;
  assignRole?: ManagementPermission<RoleName>;
  removeMember?: ManagementPermission<RoleName>;
  updateOrg?: boolean;
  deleteOrg?: boolean;
  transferOwnership?: boolean;
}

export type ManagementDefinition<RoleName extends string = string> = Partial<Record<RoleName, ManagementPolicy<RoleName>>>;

export interface GlobalDefinition<
  Schema extends SchemaDefinition = SchemaDefinition,
  Access extends AccessDefinition = AccessDefinition,
> {
  name?: string;
  type: "global";
  schema: Schema;
  access: Access;
}

export interface UserDefinition<
  Schema extends SchemaDefinition = SchemaDefinition,
  Access extends AccessDefinition = AccessDefinition,
> {
  name?: string;
  type: "user";
  schema: Schema;
  access: Access;
}

export interface OrganizationDefinition<
  RoleName extends string = string,
  Schema extends SchemaDefinition = SchemaDefinition,
  Access extends AccessDefinition = AccessDefinition,
> {
  name?: string;
  type: "organization";
  roles?: readonly RoleName[];
  maxMembers?: number;
  management?: ManagementDefinition<RoleName>;
  schema: Schema;
  access: Access;
}

export type DefinitionDefinition =
  | GlobalDefinition
  | UserDefinition
  | OrganizationDefinition;

type FieldReference<Path extends string = string> = {
  __kind: "field_ref";
  path: Path;
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
export class TableBuilder<Columns extends Record<string, ColumnBuilder> = Record<string, ColumnBuilder>> {
  private _columns: Columns;
  private _indexes: IndexDefinition[] = [];
  private _ftsColumns: string[] | undefined = undefined;

  constructor(columns: Columns) {
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
export function defineTable<const Columns extends Record<string, ColumnBuilder>>(
  columns: Columns
): TableBuilder<Columns> {
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
type SchemaTableMap = Record<string, TableBuilder<any>>;
type TypedSchema<Tables extends SchemaTableMap = SchemaTableMap> = SchemaDefinition & {
  readonly __tables?: Tables;
};

export function defineSchema<const Tables extends SchemaTableMap>(name: string, tables: Tables): TypedSchema<Tables>;
export function defineSchema<const Tables extends SchemaTableMap>(tables: Tables): TypedSchema<Tables>;
export function defineSchema(
  nameOrTables: string | SchemaTableMap,
  maybeTables?: SchemaTableMap
): TypedSchema {
  const name = typeof nameOrTables === "string" ? nameOrTables : undefined;
  const tables = typeof nameOrTables === "string" ? maybeTables ?? {} : nameOrTables;
  const tableDefinitions: TableDefinition[] = [];

  for (const [tableName, builder] of Object.entries(tables)) {
    tableDefinitions.push(builder._build(tableName));
  }

  return {
    name,
    tables: tableDefinitions,
  } as TypedSchema;
}

function field<Path extends string>(path: Path): FieldReference<Path> {
  return { __kind: "field_ref", path };
}

function isFieldReference<Path extends string = string>(value: unknown): value is FieldReference<Path> {
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

function createScopeProxy<Scope extends string, Fields extends string = string>(
  scope: Scope
): { [K in Fields]: FieldReference<`${Scope}.${K}`> } {
  return new Proxy(
    {},
    {
      get(_target, prop) {
        if (typeof prop !== "string") return undefined;
        return field(`${scope}.${prop}`);
      },
    }
  ) as { [K in Fields]: FieldReference<`${Scope}.${K}`> };
}

type TableMapOf<Schema extends TypedSchema<any>> = NonNullable<Schema["__tables"]>;
type TableNameOf<Schema extends TypedSchema<any>> = Extract<keyof TableMapOf<Schema>, string>;
type ColumnNameOf<
  Schema extends TypedSchema<any>,
  TableName extends TableNameOf<Schema>,
> = TableMapOf<Schema>[TableName] extends TableBuilder<infer Columns>
  ? Extract<keyof Columns, string>
  : never;

type AuthFieldName = "id" | "status" | "role";
type OperationName = "select" | "insert" | "update" | "delete";
type EmptyScope = Record<never, never>;
type PrevFieldPath<RowField extends string, Op extends OperationName> = Op extends "insert" ? never : `old.${RowField}`;
type NextFieldPath<RowField extends string, Op extends OperationName> = Op extends "select" | "delete" ? never : `new.${RowField}`;
type PolicyFieldPath<RowField extends string, Op extends OperationName> =
  | `auth.${AuthFieldName}`
  | PrevFieldPath<RowField, Op>
  | NextFieldPath<RowField, Op>;

type PolicyContext<RowField extends string, Op extends OperationName> = {
  auth: { [K in AuthFieldName]: FieldReference<`auth.${K}`> };
  prev: Op extends "insert" ? EmptyScope : { [K in RowField]: FieldReference<`old.${K}`> };
  next: Op extends "select" | "delete" ? EmptyScope : { [K in RowField]: FieldReference<`new.${K}`> };
};

type PolicyValue<RowField extends string, Op extends OperationName> =
  | Condition<PolicyFieldPath<RowField, Op>>
  | ((ctx: PolicyContext<RowField, Op>) => Condition<PolicyFieldPath<RowField, Op>>);

type PolicyInput<RowField extends string> = {
  select?: PolicyValue<RowField, "select">;
  insert?: PolicyValue<RowField, "insert">;
  update?: PolicyValue<RowField, "update">;
  delete?: PolicyValue<RowField, "delete">;
};

type AccessInput<Schema extends TypedSchema<any>> = {
  [TableName in TableNameOf<Schema>]?: PolicyInput<ColumnNameOf<Schema, TableName>>;
};

type ManagementRoleContext<RoleName extends string> = {
  [K in RoleName as K extends "any" ? never : K]: RoleReference<K>;
} & {
  any(): { any: true };
};

type ManagementResolver<RoleName extends string> =
  | ManagementDefinition<RoleName>
  | ((role: ManagementRoleContext<RoleName>) => ManagementDefinition<RoleName>);

export interface MembershipDefinition<
  RoleName extends string = string,
  Roles extends readonly RoleName[] = readonly RoleName[],
> {
  roles: Roles;
  management?: ManagementResolver<RoleName>;
}

export function allow<FieldPath extends string = string>(): Condition<FieldPath> {
  return {};
}

export function eq<LeftPath extends string, RightPath extends string>(
  left: FieldReference<LeftPath> | unknown,
  right: FieldReference<RightPath> | unknown
): Condition<LeftPath | RightPath> {
  if (isFieldReference(left)) {
    return { field: left.path as LeftPath | RightPath, op: "eq", value: serializeValue(right) };
  }
  if (isFieldReference(right)) {
    return { field: right.path as LeftPath | RightPath, op: "eq", value: serializeValue(left) };
  }
  throw new Error("eq requires at least one field reference");
}

export function inList<Path extends string>(
  left: FieldReference<Path> | unknown,
  values: unknown[]
): Condition<Path> {
  if (!isFieldReference(left)) {
    throw new Error("inList requires a field reference as the first argument");
  }
  return { field: left.path as Path, op: "in", value: values.map((value) => serializeValue(value)) };
}

export function and<FieldPath extends string>(...conditions: Condition<FieldPath>[]): Condition<FieldPath> {
  return { and: conditions };
}

export function or<FieldPath extends string>(...conditions: Condition<FieldPath>[]): Condition<FieldPath> {
  return { or: conditions };
}

export function not<FieldPath extends string>(condition: Condition<FieldPath>): Condition<FieldPath> {
  return { not: condition };
}

function resolvePolicyValue<RowField extends string, Op extends OperationName>(
  operation: Op,
  value: PolicyValue<RowField, Op> | undefined
): Condition<PolicyFieldPath<RowField, Op>> | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (typeof value !== "function") {
    return value;
  }
  const prev = operation === "insert" ? {} : createScopeProxy<"old", RowField>("old");
  const next = operation === "select" || operation === "delete" ? {} : createScopeProxy<"new", RowField>("new");
  return value({
    auth: createScopeProxy<"auth", AuthFieldName>("auth"),
    prev,
    next,
  } as PolicyContext<RowField, Op>);
}

export function defineAccess<Schema extends TypedSchema<any>>(
  schema: Schema,
  access: AccessInput<Schema>
): AccessDefinition<
  TableNameOf<Schema>,
  PolicyFieldPath<ColumnNameOf<Schema, TableNameOf<Schema>>, OperationName>
> {
  void schema;
  const normalized: AccessDefinition<
    TableNameOf<Schema>,
    PolicyFieldPath<ColumnNameOf<Schema, TableNameOf<Schema>>, OperationName>
  > = {};

  for (const tableName in access) {
    const policy = access[tableName as TableNameOf<Schema>];
    if (!policy) continue;
    normalized[tableName as TableNameOf<Schema>] = {
      select: resolvePolicyValue("select", policy.select),
      insert: resolvePolicyValue("insert", policy.insert),
      update: resolvePolicyValue("update", policy.update),
      delete: resolvePolicyValue("delete", policy.delete),
    };
  }

  return normalized;
}

export function defineMembership<const Roles extends readonly string[]>(
  input: {
    roles: Roles;
    management?: ManagementResolver<Roles[number]>;
  }
): MembershipDefinition<Roles[number], Roles> {
  return input;
}

function normalizeManagement<RoleName extends string>(input: ManagementDefinition<RoleName>): ManagementDefinition<RoleName> {
  const out: ManagementDefinition<RoleName> = {};
  for (const roleName in input) {
    const policy = input[roleName as RoleName];
    if (!policy) continue;
    out[roleName as RoleName] = {
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

function normalizeManagementPermission<RoleName extends string>(permission: ManagementPermission<RoleName> | undefined): ManagementPermission<RoleName> | undefined {
  if (permission === undefined || typeof permission === "boolean") {
    return permission;
  }
  if (typeof permission === "string") {
    return permission;
  }
  if (Array.isArray(permission)) {
    return permission.map((item) => normalizeRoleValue(item)) as RoleName[];
  }
  if (typeof permission === "object" && permission !== null && "__kind" in permission && (permission as { __kind?: string }).__kind === "role_ref") {
    return normalizeRoleValue(permission) as RoleName;
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

function createManagementRoleProxy<RoleName extends string>(): ManagementRoleContext<RoleName> {
  return new Proxy(
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
  ) as ManagementRoleContext<RoleName>;
}

export function defineOrg<const Roles extends readonly string[] = readonly string[]>(
  input: Omit<OrganizationDefinition<Roles[number]>, "type" | "management" | "roles"> & {
    membership?: MembershipDefinition<Roles[number], Roles>;
  }
): OrganizationDefinition<Roles[number]> {
  const { membership, ...rest } = input;
  const rawRoles = membership?.roles;
  const rawManagement = membership?.management;
  let management: ManagementDefinition<Roles[number]> | undefined;
  if (typeof rawManagement === "function") {
    management = normalizeManagement(rawManagement(createManagementRoleProxy()));
  } else {
    management = rawManagement as ManagementDefinition<Roles[number]> | undefined;
  }
  return {
    ...rest,
    roles: rawRoles,
    management,
    type: "organization",
  };
}
