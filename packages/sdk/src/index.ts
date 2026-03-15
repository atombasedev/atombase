// =============================================================================
// Client
// =============================================================================

export { AtomicbaseClient, DatabaseClient, createClient } from "./AtomicbaseClient.js";
export { DatabasesClient } from "./DatabasesClient.js";
export { DefinitionsClient } from "./DefinitionsClient.js";
export { AuthClient, OrganizationAuthClient } from "./AuthClient.js";

// =============================================================================
// Builders (for advanced usage / extension)
// =============================================================================

export { AtomicbaseBuilder } from "./AtomicbaseBuilder.js";
export { AtomicbaseQueryBuilder } from "./AtomicbaseQueryBuilder.js";

// =============================================================================
// Error
// =============================================================================

export { AtomicbaseError } from "./AtomicbaseError.js";

// =============================================================================
// Filter Functions (for complex conditions with where())
// =============================================================================

export {
  // Column reference helper
  col,
  // Join condition functions
  onEq,
  onNeq,
  onGt,
  onGte,
  onLt,
  onLte,
  // WHERE filter functions (for use with .where())
  eq,
  neq,
  gt,
  gte,
  lt,
  lte,
  like,
  glob,
  inList,
  notInList,
  between,
  isNull,
  isNotNull,
  fts,
  not,
  or,
  and,
} from "./filters.js";

// =============================================================================
// Types
// =============================================================================

export type {
  // Response types (discriminated unions)
  AtomicbaseResponse,
  AtomicbaseResponseSuccess,
  AtomicbaseResponseFailure,
  AtomicbaseResponseWithCount,
  // Batch types
  BatchOperation,
  BatchResponse,
  AtomicbaseBatchResponse,
  // Configuration
  AtomicbaseClientOptions,
  DefinitionType,
  GeneratedColumn,
  ColumnDefinition,
  IndexDefinition,
  TableDefinition,
  SchemaDefinition,
  Condition,
  OperationPolicy,
  AccessDefinition,
  ManagementPermission,
  ManagementPolicy,
  ManagementDefinition,
  Definition,
  DefinitionVersion,
  Merge,
  CreateDefinitionOptions,
  PushDefinitionOptions,
  // Query types
  FilterCondition,
  SelectColumn,
  OrderDirection,
  // Join types
  JoinClause,
  // Database types (Platform API)
  Database,
  CreateDatabaseOptions,
  User,
  MagicLinkStartOptions,
  MagicLinkStartResponse,
  MagicLinkCompleteResponse,
  CreateUserDatabaseOptions,
  Organization,
  CreateOrganizationOptions,
  OrganizationInvite,
  CreateOrganizationInviteOptions,
  OrganizationMember,
  CreateOrganizationMemberOptions,
  UpdateOrganizationMemberOptions,
  UpdateOrganizationOptions,
  TransferOrganizationOwnershipOptions,
} from "./types.js";
