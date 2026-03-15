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
  /** API key for authentication */
  apiKey?: string;
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
  organizationId?: string;
  organizationName?: string;
  ownerId?: string;
  maxMembers?: number;
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
