import { AtomicbaseError } from "./AtomicbaseError.js";
import type {
  AtomicbaseResponse,
  CreateDatabaseOptions,
  Database,
} from "./types.js";

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
      return { data: null, error: AtomicbaseError.networkError(err) };
    }
  }

  async get(id: string): Promise<AtomicbaseResponse<Database>> {
    try {
      const response = await this.fetchFn(
        `${this.baseUrl}/platform/databases/${encodeURIComponent(id)}`,
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
      return { data: null, error: AtomicbaseError.networkError(err) };
    }
  }

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
      return { data: null, error: AtomicbaseError.networkError(err) };
    }
  }

  async delete(id: string): Promise<AtomicbaseResponse<void>> {
    try {
      const response = await this.fetchFn(
        `${this.baseUrl}/platform/databases/${encodeURIComponent(id)}`,
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

      return { data: undefined as void, error: null };
    } catch (err) {
      return { data: null, error: AtomicbaseError.networkError(err) };
    }
  }
}
