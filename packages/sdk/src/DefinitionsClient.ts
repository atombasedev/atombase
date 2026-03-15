import { AtomicbaseError } from "./AtomicbaseError.js";
import type {
  AtomicbaseResponse,
  CreateDefinitionOptions,
  Definition,
  DefinitionVersion,
  PushDefinitionOptions,
} from "./types.js";

export class DefinitionsClient {
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
      headers.Authorization = `Bearer service.${this.apiKey}`;
    }

    return headers;
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<AtomicbaseResponse<T>> {
    try {
      const response = await this.fetchFn(`${this.baseUrl}${path}`, {
        method,
        headers: this.getHeaders(),
        body: body ? JSON.stringify(body) : undefined,
      });

      if (!response.ok) {
        const errorBody = await response.json().catch(() => ({}));
        return { data: null, error: AtomicbaseError.fromResponse(errorBody, response.status) };
      }

      const text = await response.text();
      return { data: (text ? JSON.parse(text) : null) as T, error: null };
    } catch (err) {
      return { data: null, error: AtomicbaseError.networkError(err) };
    }
  }

  list(): Promise<AtomicbaseResponse<Definition[]>> {
    return this.request("GET", "/platform/definitions");
  }

  get(name: string): Promise<AtomicbaseResponse<Definition>> {
    return this.request("GET", `/platform/definitions/${encodeURIComponent(name)}`);
  }

  create(options: CreateDefinitionOptions): Promise<AtomicbaseResponse<Definition>> {
    return this.request("POST", "/platform/definitions", options);
  }

  push(name: string, options: PushDefinitionOptions): Promise<AtomicbaseResponse<DefinitionVersion>> {
    return this.request("POST", `/platform/definitions/${encodeURIComponent(name)}/push`, options);
  }

  history(name: string): Promise<AtomicbaseResponse<DefinitionVersion[]>> {
    return this.request("GET", `/platform/definitions/${encodeURIComponent(name)}/history`);
  }
}
