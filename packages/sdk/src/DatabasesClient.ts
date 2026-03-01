import { AtomicbaseError } from "./AtomicbaseError.js";
import type {
  AtomicbaseResponse,
  Database,
  CreateDatabaseOptions,
  SyncDatabaseResponse,
} from "./types.js";

/**
 * Client for managing databases via the Platform API.
 *
 * @example
 * ```ts
 * const client = createClient({ url: 'http://localhost:8080', apiKey: 'key' })
 *
 * // List all databases
 * const { data: databases } = await client.databases.list()
 *
 * // Create a new database
 * const { data: database } = await client.databases.create({
 *   name: 'acme-corp',
 *   template: 'my-app'
 * })
 *
 * // Get database details
 * const { data: database } = await client.databases.get('acme-corp')
 *
 * // Sync database to latest template version
 * const { data: result } = await client.databases.sync('acme-corp')
 *
 * // Delete a database
 * const { error } = await client.databases.delete('acme-corp')
 * ```
 */
export class DatabasesClient {
  private readonly baseUrl: string;
  private readonly apiKey?: string;
  private readonly headers: Record<string, string>;
  private readonly fetchFn: typeof fetch;

  constructor(options: {
    baseUrl: string;
    apiKey?: string;
    headers: Record<string, string>;
    fetch: typeof fetch;
  }) {
    this.baseUrl = options.baseUrl;
    this.apiKey = options.apiKey;
    this.headers = options.headers;
    this.fetchFn = options.fetch;
  }

  private getHeaders(): Record<string, string> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...this.headers,
    };

    if (this.apiKey) {
      headers["Authorization"] = `Bearer service.${this.apiKey}`;
    }

    return headers;
  }

  /**
   * List all databases.
   *
   * @example
   * ```ts
   * const { data, error } = await client.databases.list()
   * if (error) {
   *   console.error('Failed to list databases:', error.message)
   * } else {
   *   console.log('Databases:', data)
   * }
   * ```
   */
  async list(): Promise<AtomicbaseResponse<Database[]>> {
    try {
      const response = await this.fetchFn(`${this.baseUrl}/platform/databases`, {
        method: "GET",
        headers: this.getHeaders(),
      });

      if (!response.ok) {
        const errorBody = await response.json().catch(() => ({}));
        const error = AtomicbaseError.fromResponse(errorBody, response.status);
        return { data: null, error };
      }

      const data = await response.json();
      return { data, error: null };
    } catch (err) {
      const error = AtomicbaseError.networkError(err);
      return { data: null, error };
    }
  }

  /**
   * Get a database by name.
   *
   * @example
   * ```ts
   * const { data, error } = await client.databases.get('acme-corp')
   * if (error) {
   *   console.error('Database not found:', error.message)
   * } else {
   *   console.log('Database:', data)
   * }
   * ```
   */
  async get(name: string): Promise<AtomicbaseResponse<Database>> {
    try {
      const response = await this.fetchFn(
        `${this.baseUrl}/platform/databases/${encodeURIComponent(name)}`,
        {
          method: "GET",
          headers: this.getHeaders(),
        }
      );

      if (!response.ok) {
        const errorBody = await response.json().catch(() => ({}));
        const error = AtomicbaseError.fromResponse(errorBody, response.status);
        return { data: null, error };
      }

      const data = await response.json();
      return { data, error: null };
    } catch (err) {
      const error = AtomicbaseError.networkError(err);
      return { data: null, error };
    }
  }

  /**
  * Create a new database from a template.
   *
   * @example
   * ```ts
   * const { data, error } = await client.databases.create({
   *   name: 'acme-corp',
   *   template: 'my-app'
   * })
   * if (error) {
   *   console.error('Failed to create database:', error.message)
   * } else {
   *   console.log('Created database:', data.name)
   * }
   * ```
   */
  async create(options: CreateDatabaseOptions): Promise<AtomicbaseResponse<Database>> {
    try {
      const response = await this.fetchFn(`${this.baseUrl}/platform/databases`, {
        method: "POST",
        headers: this.getHeaders(),
        body: JSON.stringify(options),
      });

      if (!response.ok) {
        const errorBody = await response.json().catch(() => ({}));
        const error = AtomicbaseError.fromResponse(errorBody, response.status);
        return { data: null, error };
      }

      const data = await response.json();
      return { data, error: null };
    } catch (err) {
      const error = AtomicbaseError.networkError(err);
      return { data: null, error };
    }
  }

  /**
  * Delete a database.
   *
   * @example
   * ```ts
   * const { error } = await client.databases.delete('acme-corp')
   * if (error) {
   *   console.error('Failed to delete database:', error.message)
   * } else {
   *   console.log('Database deleted')
   * }
   * ```
   */
  async delete(name: string): Promise<AtomicbaseResponse<void>> {
    try {
      const response = await this.fetchFn(
        `${this.baseUrl}/platform/databases/${encodeURIComponent(name)}`,
        {
          method: "DELETE",
          headers: this.getHeaders(),
        }
      );

      if (!response.ok) {
        const errorBody = await response.json().catch(() => ({}));
        const error = AtomicbaseError.fromResponse(errorBody, response.status);
        return { data: null, error };
      }

      // 204 No Content on success
      return { data: undefined as unknown as void, error: null };
    } catch (err) {
      const error = AtomicbaseError.networkError(err);
      return { data: null, error };
    }
  }

  /**
   * Sync a database to the latest template version.
   * This applies any pending schema migrations to the database.
   *
   * @example
   * ```ts
   * const { data, error } = await client.databases.sync('acme-corp')
   * if (error) {
   *   if (error.code === 'DATABASE_IN_SYNC') {
   *     console.log('Database is already up to date')
   *   } else {
   *     console.error('Sync failed:', error.message)
   *   }
   * } else {
   *   console.log(`Synced from v${data.fromVersion} to v${data.toVersion}`)
   * }
   * ```
   */
  async sync(name: string): Promise<AtomicbaseResponse<SyncDatabaseResponse>> {
    try {
      const response = await this.fetchFn(
        `${this.baseUrl}/platform/databases/${encodeURIComponent(name)}/sync`,
        {
          method: "POST",
          headers: this.getHeaders(),
        }
      );

      if (!response.ok) {
        const errorBody = await response.json().catch(() => ({}));
        const error = AtomicbaseError.fromResponse(errorBody, response.status);
        return { data: null, error };
      }

      const data = await response.json();
      return { data, error: null };
    } catch (err) {
      const error = AtomicbaseError.networkError(err);
      return { data: null, error };
    }
  }
}
