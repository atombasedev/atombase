import type { SchemaDefinition, TableDefinition, ColumnDefinition, IndexDefinition } from "@atomicbase/template";
import type { AtomicbaseConfig } from "./config.js";

// Re-export types from schema package (these match Go API types directly)
export type { TableDefinition, ColumnDefinition, IndexDefinition };

// Custom error class to preserve HTTP status code and API error details
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code?: string,
    public hint?: string
  ) {
    super(message);
    this.name = "ApiError";
  }

  /** Format the error for display, including hint if available */
  format(): string {
    let result = this.message;
    if (this.hint) {
      result += `\n\nHint: ${this.hint}`;
    }
    return result;
  }
}

// Schema type matches Go API's Schema type
export interface Schema {
  tables: TableDefinition[];
}

// SchemaDiff represents a single schema modification (matches Go API)
export interface SchemaDiff {
  type: string;  // add_table, drop_table, add_column, drop_column, etc.
  table?: string;
  column?: string;
}

// DiffResult is returned by the Diff endpoint (matches Go API)
export interface DiffResult {
  changes: SchemaDiff[];
}

// Merge indicates a drop+add pair that should be treated as a rename (matches Go API)
export interface Merge {
  old: number;  // Index of drop statement in changes array
  new: number;  // Index of add statement in changes array
}

// MigrateResponse is returned by the migrate endpoint (matches Go API)
export interface MigrateResponse {
  templateId: number;
  currentVersion: number;
  databasesTotal: number;
  status: string;
}

// TemplateListItem matches Go API's Template (list response without schema)
export interface TemplateListItem {
  id: number;
  name: string;
  currentVersion: number;
  createdAt: string;
  updatedAt: string;
}

// TemplateWithSchema matches Go API's TemplateWithSchema
export interface TemplateResponse {
  id: number;
  name: string;
  currentVersion: number;
  createdAt: string;
  updatedAt: string;
  schema: Schema;
}

// PushResponse for creating new templates
export interface PushResponse {
  id: number;
  name: string;
  currentVersion: number;
  createdAt: string;
  updatedAt: string;
  schema: Schema;
}

// TemplateVersion represents a version in template history (matches Go API)
export interface TemplateVersion {
  id: number;
  templateId: number;
  version: number;
  schema: Schema;
  checksum: string;
  createdAt: string;
}

// RollbackResponse is returned by the rollback endpoint (matches Go API)
export type RollbackResponse = MigrateResponse;

// Database represents a database record (matches Go API)
export interface Database {
  id: number;
  name: string;
  token?: string;  // Omitted in list responses
  templateId: number;
  templateVersion: number;
  createdAt: string;
  updatedAt: string;
}

// SyncDatabaseResponse is returned by the sync endpoint (matches Go API)
export interface SyncDatabaseResponse {
  fromVersion: number;
  toVersion: number;
}

export class ApiClient {
  private baseUrl: string;
  private apiKey?: string;
  private insecure: boolean;

  constructor(config: Required<AtomicbaseConfig>) {
    this.baseUrl = config.url.replace(/\/$/, "");
    this.apiKey = config.apiKey || undefined;
    this.insecure = config.insecure ?? false;
  }

  private async request<T>(
    method: string,
    path: string,
    body?: unknown
  ): Promise<T> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };

    if (this.apiKey) {
      headers["Authorization"] = `Bearer ${this.apiKey}`;
    }

    // Temporarily disable SSL verification if insecure mode is enabled
    const originalTlsSetting = process.env.NODE_TLS_REJECT_UNAUTHORIZED;
    if (this.insecure) {
      process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
    }

    let response: Response;
    try {
      response = await fetch(`${this.baseUrl}${path}`, {
        method,
        headers,
        body: body ? JSON.stringify(body) : undefined,
      });
    } finally {
      // Restore original setting
      if (this.insecure) {
        if (originalTlsSetting === undefined) {
          delete process.env.NODE_TLS_REJECT_UNAUTHORIZED;
        } else {
          process.env.NODE_TLS_REJECT_UNAUTHORIZED = originalTlsSetting;
        }
      }
    }

    if (!response.ok) {
      const error = await response.json().catch(() => ({}));
      throw new ApiError(
        error.message || `API error: ${response.status} ${response.statusText}`,
        response.status,
        error.code,
        error.hint
      );
    }

    // Handle empty responses (e.g., 204 No Content from DELETE)
    const text = await response.text();
    if (!text) {
      return undefined as T;
    }
    return JSON.parse(text);
  }

  /**
   * Push a schema to the server (create new template).
   * Schema package outputs API-compatible format directly.
   */
  async pushSchema(schema: SchemaDefinition): Promise<PushResponse> {
    return this.request<PushResponse>("POST", "/platform/templates", {
      name: schema.name,
      schema: { tables: schema.tables },
    });
  }

  /**
   * Migrate an existing template to a new schema.
   * Returns lazy migration summary information.
   */
  async migrateTemplate(
    name: string,
    schema: SchemaDefinition,
    merges?: Merge[]
  ): Promise<MigrateResponse> {
    return this.request<MigrateResponse>("POST", `/platform/templates/${name}/migrate`, {
      schema: { tables: schema.tables },
      merge: merges,
    });
  }

  /**
   * Get a template by name.
   */
  async getTemplate(name: string): Promise<TemplateResponse> {
    return this.request<TemplateResponse>("GET", `/platform/templates/${name}`);
  }

  /**
   * List all templates.
   */
  async listTemplates(): Promise<TemplateListItem[]> {
    return this.request<TemplateListItem[]>("GET", "/platform/templates");
  }

  /**
   * Preview changes without applying (diff).
   * Returns raw changes - ambiguity detection is client-side.
   */
  async diffSchema(
    name: string,
    schema: SchemaDefinition
  ): Promise<DiffResult> {
    return this.request<DiffResult>("POST", `/platform/templates/${name}/diff`, {
      schema: { tables: schema.tables },
    });
  }

  /**
   * Check if a template exists.
   * Returns false only for 404 errors, re-throws other errors.
   */
  async templateExists(name: string): Promise<boolean> {
    try {
      await this.getTemplate(name);
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        return false;
      }
      throw err;
    }
  }

  /**
   * Delete a template.
   * Only succeeds if no databases are using it.
   */
  async deleteTemplate(name: string): Promise<void> {
    await this.request<void>("DELETE", `/platform/templates/${name}`);
  }

  /**
   * Get version history for a template.
   */
  async getTemplateHistory(name: string): Promise<TemplateVersion[]> {
    return this.request<TemplateVersion[]>("GET", `/platform/templates/${name}/history`);
  }

  /**
   * Rollback a template to a previous version.
   * Returns lazy migration summary information.
   */
  async rollbackTemplate(name: string, version: number): Promise<RollbackResponse> {
    return this.request<RollbackResponse>("POST", `/platform/templates/${name}/rollback`, {
      version,
    });
  }

  // =========================================================================
  // Database Management
  // =========================================================================

  /**
   * List all databases.
   */
  async listDatabases(): Promise<Database[]> {
    return this.request<Database[]>("GET", "/platform/databases");
  }

  /**
   * Get a database by name.
   */
  async getDatabase(name: string): Promise<Database> {
    return this.request<Database>("GET", `/platform/databases/${name}`);
  }

  /**
   * Create a new database.
   */
  async createDatabase(name: string, template: string): Promise<Database> {
    return this.request<Database>("POST", "/platform/databases", {
      name,
      template,
    });
  }

  /**
   * Delete a database.
   */
  async deleteDatabase(name: string): Promise<void> {
    await this.request<void>("DELETE", `/platform/databases/${name}`);
  }

  /**
   * Sync a database to the latest template version.
   */
  async syncDatabase(name: string): Promise<SyncDatabaseResponse> {
    return this.request<SyncDatabaseResponse>("POST", `/platform/databases/${name}/sync`);
  }
}
