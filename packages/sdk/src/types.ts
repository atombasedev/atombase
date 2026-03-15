// =============================================================================
// Response Types - Discriminated unions for type-safe error handling
// =============================================================================

import type { AtomicbaseError } from "./AtomicbaseError.js";

/**
 * Successful response with data and no error.
 */
export interface AtomicbaseResponseSuccess<T> {
  data: T;
  error: null;
}

/**
 * Failed response with error and no data.
 */
export interface AtomicbaseResponseFailure {
  data: null;
  error: AtomicbaseError;
}

/**
 * Generic response that is either success or failure.
 * Use type narrowing on `error` to discriminate:
 * ```ts
 * const { data, error } = await client.from('users').select()
 * if (error) {
 *   // error is AtomicbaseError, data is null
 * } else {
 *   // data is T[], error is null
 * }
 * ```
 */
export type AtomicbaseResponse<T> =
  | AtomicbaseResponseSuccess<T>
  | AtomicbaseResponseFailure;

/**
 * Response with count - includes total count alongside data.
 */
export interface AtomicbaseResponseWithCount<T> {
  data: T | null;
  count: number | null;
  error: AtomicbaseError | null;
}

// =============================================================================
// Configuration Types
// =============================================================================

export interface AtomicbaseClientOptions {
  /** Base URL of the Atomicbase API */
  url: string;
  /** Service API key for platform/admin requests */
  apiKey?: string;
  /** Session token for user-scoped auth and data requests */
  sessionToken?: string;
  /** Custom fetch implementation */
  fetch?: typeof fetch;
  /** Default headers to include in all requests */
  headers?: Record<string, string>;
}

// =============================================================================
// Query Types
// =============================================================================

export type FilterCondition = Record<string, unknown>;

export type SelectColumn = string | { [relation: string]: SelectColumn[] };

export type OrderDirection = "asc" | "desc";

export type JoinType = "left" | "inner";

export type QueryOperation = "select" | "insert" | "upsert" | "update" | "delete";

export type ResultMode = "default" | "single" | "maybeSingle" | "count" | "withCount";

/**
 * Custom join clause for explicit joins.
 */
export interface JoinClause {
  /** Table to join */
  table: string;
  /** Join type: "left" (default) or "inner" */
  type?: JoinType;
  /** Join conditions using filter functions: [eq("users.id", "orders.user_id")] */
  on: FilterCondition[];
  /** Optional alias for the joined table */
  alias?: string;
  /** If true, flatten output instead of nesting (default: false) */
  flat?: boolean;
}

// =============================================================================
// Batch Types
// =============================================================================

/**
 * A single operation in a batch request.
 */
export interface BatchOperation {
  operation: string;
  table: string;
  body: Record<string, unknown>;
  /** Whether to include count in the result (for select operations) */
  count?: boolean;
  /** Result mode for client-side post-processing */
  resultMode?: ResultMode;
}

/**
 * Response from a batch request.
 */
export interface BatchResponse<T extends unknown[] = unknown[]> {
  results: T;
}

/**
 * Batch response with potential error.
 */
export type AtomicbaseBatchResponse<T extends unknown[] = unknown[]> =
  | { data: BatchResponse<T>; error: null }
  | { data: null; error: AtomicbaseError };

// =============================================================================
// Definitions Types (Platform API)
// =============================================================================

export type DefinitionType = "global" | "user" | "organization";

export interface GeneratedColumn {
  expr: string;
  stored?: boolean;
}

export interface ColumnDefinition {
  name: string;
  type: "INTEGER" | "TEXT" | "REAL" | "BLOB";
  notNull?: boolean;
  unique?: boolean;
  default?: string | number | null | { sql: string };
  collate?: "BINARY" | "NOCASE" | "RTRIM";
  check?: string;
  generated?: GeneratedColumn;
  references?: string;
  onDelete?: "CASCADE" | "SET NULL" | "RESTRICT" | "NO ACTION";
  onUpdate?: "CASCADE" | "SET NULL" | "RESTRICT" | "NO ACTION";
}

export interface IndexDefinition {
  name: string;
  columns: string[];
  unique?: boolean;
}

export interface TableDefinition {
  name: string;
  pk: string[];
  columns: Record<string, ColumnDefinition>;
  indexes?: IndexDefinition[];
  ftsColumns?: string[];
}

export interface SchemaDefinition {
  name?: string;
  tables: TableDefinition[];
}

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

export type AccessDefinition<
  TableName extends string = string,
  FieldPath extends string = string,
> = Partial<Record<TableName, OperationPolicy<FieldPath>>>;

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

export type ManagementDefinition<RoleName extends string = string> = Partial<Record<RoleName, ManagementPolicy>>;

export interface Definition {
  id: number;
  name: string;
  type: DefinitionType;
  roles?: string[];
  management?: ManagementDefinition;
  provision?: Condition;
  currentVersion: number;
  createdAt: string;
  updatedAt: string;
  schema?: SchemaDefinition;
}

export interface DefinitionVersion {
  id: number;
  definitionId: number;
  version: number;
  schema: SchemaDefinition;
  provision?: Condition;
  checksum: string;
  createdAt: string;
}

export interface Merge {
  old: number;
  new: number;
}

export interface CreateDefinitionOptions {
  name: string;
  type: DefinitionType;
  roles?: string[];
  management?: ManagementDefinition;
  provision?: Condition;
  schema: SchemaDefinition;
  access: AccessDefinition;
}

export interface PushDefinitionOptions {
  schema: SchemaDefinition;
  access: AccessDefinition;
  management?: ManagementDefinition;
  provision?: Condition;
  merge?: Merge[];
}

// =============================================================================
// Database Types (Platform API)
// =============================================================================

/**
 * Database information.
 */
export interface Database {
  id: string;
  token?: string;
  definitionId: number;
  definitionName?: string;
  definitionType?: string;
  definitionVersion: number;
  ownerId?: string;
  organizationId?: string;
  organizationName?: string;
  createdAt: string;
  updatedAt: string;
}

/**
 * Options for creating a new database.
 */
export interface CreateDatabaseOptions {
  /** Unique id for the database */
  id: string;
  /** Name of the definition to use for the database schema */
  definition: string;
  userId?: string;
}

// =============================================================================
// Auth Types
// =============================================================================

export interface User {
  id: string;
  databaseId?: string;
  email: string;
  email_verified_at?: string;
  created_at: string;
}

export interface MagicLinkStartOptions {
  email: string;
}

export interface MagicLinkStartResponse {
  message: string;
}

export interface MagicLinkCompleteResponse {
  user: User;
  token: string;
  expires_at: string;
  is_new: boolean;
}

export interface CreateUserDatabaseOptions {
  definition: string;
}

export interface OrganizationMember {
  userId: string;
  role: string;
  status: string;
  createdAt: string;
}

export interface Organization {
  id: string;
  name: string;
  ownerId: string;
  maxMembers?: number;
  metadata: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
}

export interface CreateOrganizationOptions {
  id: string;
  name: string;
  definition: string;
  ownerId?: string;
  maxMembers?: number;
  metadata?: Record<string, unknown>;
}

export interface OrganizationInvite {
  id: string;
  email: string;
  role: string;
  invitedBy: string;
  expiresAt: string;
  createdAt: string;
}

export interface CreateOrganizationInviteOptions {
  id?: string;
  email: string;
  role: string;
  expiresAt?: string;
}

export interface CreateOrganizationMemberOptions {
  userId: string;
  role: string;
  status?: string;
}

export interface UpdateOrganizationMemberOptions {
  role?: string;
  status?: string;
}

export interface UpdateOrganizationOptions {
  name?: string;
  maxMembers?: number;
  metadata?: Record<string, unknown>;
}

export interface TransferOrganizationOwnershipOptions {
  userId: string;
}
