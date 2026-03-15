import type {
  AccessDefinition,
  DefinitionDefinition,
  DefinitionType,
  ManagementDefinition,
  SchemaDefinition,
  TableDefinition,
  ColumnDefinition,
  IndexDefinition,
} from "@atomicbase/definitions";
import type { AtomicbaseConfig } from "./config.js";

export type { TableDefinition, ColumnDefinition, IndexDefinition };

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

  format(): string {
    let result = this.message;
    if (this.hint) {
      result += `\n\nHint: ${this.hint}`;
    }
    return result;
  }
}

export interface SchemaDiff {
  type: string;
  table?: string;
  column?: string;
}

export interface DiffResult {
  changes: SchemaDiff[];
}

export interface Merge {
  old: number;
  new: number;
}

export interface DefinitionResponse {
  id: number;
  name: string;
  type: DefinitionType;
  roles?: string[];
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
  checksum: string;
  createdAt: string;
}

export interface Database {
  id: string;
  token?: string;
  definitionId: number;
  definitionName?: string;
  definitionType?: string;
  definitionVersion: number;
  createdAt: string;
  updatedAt: string;
  ownerId?: string;
  organizationId?: string;
  organizationName?: string;
}

export interface CreateDatabaseInput {
  id: string;
  definition: string;
  userId?: string;
  organizationId?: string;
  organizationName?: string;
  ownerId?: string;
  maxMembers?: number;
}

type CreateDefinitionBody = {
  name: string;
  type: DefinitionType;
  roles?: string[];
  management?: ManagementDefinition;
  schema: SchemaDefinition;
  access: AccessDefinition;
};

type PushDefinitionBody = {
  schema: SchemaDefinition;
  access: AccessDefinition;
  management?: ManagementDefinition;
  merge?: Merge[];
};

function toCreateDefinitionBody(definition: DefinitionDefinition): CreateDefinitionBody {
  return {
    name: definition.name ?? "",
    type: definition.type,
    roles: definition.type === "organization" ? definition.roles : undefined,
    management: definition.type === "organization" ? definition.management : undefined,
    schema: definition.schema,
    access: definition.access,
  };
}

function toPushDefinitionBody(definition: DefinitionDefinition, merges?: Merge[]): PushDefinitionBody {
  return {
    schema: definition.schema,
    access: definition.access,
    management: definition.type === "organization" ? definition.management : undefined,
    merge: merges,
  };
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

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };

    if (this.apiKey) {
      headers["Authorization"] = `Bearer service.${this.apiKey}`;
    }

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

    const text = await response.text();
    if (!text) {
      return undefined as T;
    }
    return JSON.parse(text);
  }

  async createDefinition(definition: DefinitionDefinition): Promise<DefinitionResponse> {
    return this.request<DefinitionResponse>("POST", "/platform/definitions", toCreateDefinitionBody(definition));
  }

  async pushDefinition(name: string, definition: DefinitionDefinition, merges?: Merge[]): Promise<DefinitionVersion> {
    return this.request<DefinitionVersion>("POST", `/platform/definitions/${name}/push`, toPushDefinitionBody(definition, merges));
  }

  async getDefinition(name: string): Promise<DefinitionResponse> {
    return this.request<DefinitionResponse>("GET", `/platform/definitions/${name}`);
  }

  async listDefinitions(): Promise<DefinitionResponse[]> {
    return this.request<DefinitionResponse[]>("GET", "/platform/definitions");
  }

  async definitionExists(name: string): Promise<boolean> {
    try {
      await this.getDefinition(name);
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        return false;
      }
      throw err;
    }
  }

  async getDefinitionHistory(name: string): Promise<DefinitionVersion[]> {
    return this.request<DefinitionVersion[]>("GET", `/platform/definitions/${name}/history`);
  }

  async listDatabases(): Promise<Database[]> {
    return this.request<Database[]>("GET", "/platform/databases");
  }

  async getDatabase(id: string): Promise<Database> {
    return this.request<Database>("GET", `/platform/databases/${id}`);
  }

  async createDatabase(input: CreateDatabaseInput): Promise<Database> {
    return this.request<Database>("POST", "/platform/databases", input);
  }

  async deleteDatabase(id: string): Promise<void> {
    await this.request<void>("DELETE", `/platform/databases/${id}`);
  }
}
