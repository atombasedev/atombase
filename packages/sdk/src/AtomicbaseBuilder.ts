import { AtomicbaseError } from "./AtomicbaseError.js";
import type {
  AtomicbaseResponse,
  AtomicbaseResponseWithCount,
  FilterCondition,
  OrderDirection,
  ResultMode,
  SelectColumn,
  JoinClause,
  QueryOperation,
} from "./types.js";

/**
 * Internal query state - simplified structure.
 */
export interface QueryState {
  table: string;
  operation: QueryOperation | null;
  select: SelectColumn[];
  joins: JoinClause[];
  where: FilterCondition[];
  order: Record<string, OrderDirection> | null;
  limit: number | null;
  offset: number | null;
  data: unknown;
  returning: string[];
  onConflict: "ignore" | null;
  count: boolean;
  resultMode: ResultMode;
}

/**
 * Configuration for creating a builder.
 */
export interface BuilderConfig {
  table: string;
  baseUrl: string;
  apiKey?: string;
  sessionToken?: string;
  database?: string;
  fetch: typeof fetch;
  headers?: Record<string, string>;
}

/**
 * Base builder class that handles query construction, filtering, transforms, and execution.
 * Implements PromiseLike for lazy execution - queries only run when awaited.
 */
export abstract class AtomicbaseBuilder<T> implements PromiseLike<AtomicbaseResponse<T>> {
  protected state: QueryState;
  protected readonly baseUrl: string;
  protected readonly apiKey?: string;
  protected readonly sessionToken?: string;
  protected readonly database?: string;
  protected readonly fetchFn: typeof fetch;
  protected readonly defaultHeaders: Record<string, string>;
  protected signal?: AbortSignal;
  protected shouldThrowOnError = false;

  constructor(config: BuilderConfig) {
    this.state = {
      table: config.table,
      operation: null,
      select: [],
      joins: [],
      where: [],
      order: null,
      limit: null,
      offset: null,
      data: null,
      returning: [],
      onConflict: null,
      count: false,
      resultMode: "default",
    };
    this.baseUrl = config.baseUrl;
    this.apiKey = config.apiKey;
    this.sessionToken = config.sessionToken;
    this.database = config.database;
    this.fetchFn = config.fetch;
    this.defaultHeaders = config.headers ?? {};
  }

  // ===========================================================================
  // Filtering
  // ===========================================================================

  /**
   * Add filter conditions to the query.
   *
   * @example
   * ```ts
   * // Single condition
   * .where(eq('status', 'active'))
   *
   * // Multiple conditions (AND)
   * .where(eq('status', 'active'), gt('age', 18))
   *
   * // OR conditions
   * .where(or(eq('role', 'admin'), eq('role', 'moderator')))
   * ```
   */
  where(...conditions: FilterCondition[]): this {
    this.state.where.push(...conditions);
    return this;
  }

  // ===========================================================================
  // Ordering & Pagination
  // ===========================================================================

  /** Order results by a column. */
  orderBy(column: string, direction: OrderDirection = "asc"): this {
    this.state.order = { [column]: direction };
    return this;
  }

  /** Limit the number of rows returned. */
  limit(count: number): this {
    this.state.limit = count;
    return this;
  }

  /** Skip a number of rows before returning results. */
  offset(count: number): this {
    this.state.offset = count;
    return this;
  }

  // ===========================================================================
  // RETURNING Clause
  // ===========================================================================

  /** Specify columns to return after insert/update/delete. */
  returning(...columns: string[]): this {
    this.state.returning = columns.length > 0 ? columns : ["*"];
    return this;
  }

  // ===========================================================================
  // Result Modifiers (Type-Changing)
  // ===========================================================================

  /**
   * Return a single row. Errors if zero or multiple rows returned.
   */
  single<Result = T extends (infer U)[] ? U : T>(): AtomicbaseBuilder<Result> {
    this.state.resultMode = "single";
    if (this.state.limit === null) {
      this.state.limit = 2; // Fetch 2 to detect multiple rows
    }
    return this as unknown as AtomicbaseBuilder<Result>;
  }

  /**
   * Return zero or one row. Returns null if no rows found.
   */
  maybeSingle<Result = T extends (infer U)[] ? U | null : T | null>(): AtomicbaseBuilder<Result> {
    this.state.resultMode = "maybeSingle";
    if (this.state.limit === null) {
      this.state.limit = 1;
    }
    return this as unknown as AtomicbaseBuilder<Result>;
  }

  /**
   * Return only the count of matching rows.
   */
  count(): AtomicbaseBuilder<number> {
    this.state.resultMode = "count";
    this.state.count = true;
    this.state.limit = 0;
    return this as unknown as AtomicbaseBuilder<number>;
  }

  /**
   * Return both data and total count.
   */
  withCount(): AtomicbaseBuilder<T> & PromiseLike<AtomicbaseResponseWithCount<T>> {
    this.state.resultMode = "withCount";
    this.state.count = true;
    return this as unknown as AtomicbaseBuilder<T> & PromiseLike<AtomicbaseResponseWithCount<T>>;
  }

  // ===========================================================================
  // Request Options
  // ===========================================================================

  /** Set an AbortSignal to cancel the request. */
  abortSignal(signal: AbortSignal): this {
    this.signal = signal;
    return this;
  }

  /** Throw errors instead of returning them in the response. */
  throwOnError(): this {
    this.shouldThrowOnError = true;
    return this;
  }

  // ===========================================================================
  // Batch Support
  // ===========================================================================

  /**
   * Export this query as a batch operation.
   * @internal
   */
  toBatchOperation(): {
    operation: string;
    table: string;
    body: Record<string, unknown>;
    count?: boolean;
    resultMode?: string;
  } {
    if (!this.state.operation) {
      throw new Error("No operation specified. Call select(), insert(), update(), or delete() first.");
    }

    const result: {
      operation: string;
      table: string;
      body: Record<string, unknown>;
      count?: boolean;
      resultMode?: string;
    } = {
      operation: this.state.operation,
      table: this.state.table,
      body: this.buildBody(),
    };

    if (this.state.count) {
      result.count = true;
    }

    if (this.state.resultMode !== "default") {
      result.resultMode = this.state.resultMode;
    }

    return result;
  }

  // ===========================================================================
  // Promise Implementation (Lazy Execution)
  // ===========================================================================

  then<TResult1 = AtomicbaseResponse<T>, TResult2 = never>(
    onfulfilled?: ((value: AtomicbaseResponse<T>) => TResult1 | PromiseLike<TResult1>) | null,
    onrejected?: ((reason: unknown) => TResult2 | PromiseLike<TResult2>) | null
  ): Promise<TResult1 | TResult2> {
    return this.executeWithResultMode().then(onfulfilled, onrejected);
  }

  // ===========================================================================
  // Internal: Execution
  // ===========================================================================

  private async executeWithResultMode(): Promise<AtomicbaseResponse<T>> {
    const { resultMode } = this.state;
    const needsCount = resultMode === "count" || resultMode === "withCount";

    const result = await this.execute(needsCount);

    if (result.error) {
      if (this.shouldThrowOnError) throw result.error;
      return { data: null, error: result.error };
    }

    // Post-process based on resultMode
    switch (resultMode) {
      case "count":
        return { data: (result.count ?? 0) as T, error: null };

      case "withCount":
        return result as unknown as AtomicbaseResponse<T>;

      case "single": {
        const data = result.data as unknown[];
        if (!data || data.length === 0) {
          const error = new AtomicbaseError({
            message: "No rows returned",
            code: "NOT_FOUND",
            status: 404,
            hint: "The query returned no results. Check your filter conditions.",
          });
          if (this.shouldThrowOnError) throw error;
          return { data: null, error };
        }
        if (data.length > 1) {
          const error = new AtomicbaseError({
            message: "Multiple rows returned",
            code: "MULTIPLE_ROWS",
            status: 400,
            hint: "Expected a single row but got multiple. Add more specific filters.",
          });
          if (this.shouldThrowOnError) throw error;
          return { data: null, error };
        }
        return { data: data[0] as T, error: null };
      }

      case "maybeSingle": {
        const data = result.data as unknown[];
        return { data: (data?.[0] ?? null) as T, error: null };
      }

      default:
        return { data: result.data as T, error: null };
    }
  }

  /**
   * Execute the request. Unified method for both regular and count queries.
   */
  private async execute(withCount: boolean): Promise<AtomicbaseResponseWithCount<T>> {
    const { url, headers, body } = this.buildRequest();

    try {
      const response = await this.fetchFn(url, {
        method: "POST",
        headers,
        body: JSON.stringify(body),
        signal: this.signal,
      });

      if (!response.ok) {
        const errorBody = await response.json().catch(() => ({}));
        const error = AtomicbaseError.fromResponse(errorBody, response.status);
        if (this.shouldThrowOnError) throw error;
        return { data: null, count: null, error };
      }

      const count = withCount
        ? parseInt(response.headers.get("X-Total-Count") ?? "", 10) || null
        : null;

      const text = await response.text();
      const data = text ? JSON.parse(text) : null;

      return { data, count, error: null };
    } catch (err) {
      if (err instanceof AtomicbaseError) throw err;

      if (err instanceof DOMException && err.name === "AbortError") {
        const error = new AtomicbaseError({
          message: "Request was aborted",
          code: "ABORTED",
          status: 0,
          hint: "The request was canceled via AbortSignal",
        });
        if (this.shouldThrowOnError) throw error;
        return { data: null, count: null, error };
      }

      const error = AtomicbaseError.networkError(err);
      if (this.shouldThrowOnError) throw error;
      return { data: null, count: null, error };
    }
  }

  // ===========================================================================
  // Internal: Request Building (implemented by subclass)
  // ===========================================================================

  protected abstract buildBody(): Record<string, unknown>;

  protected buildRequest(): { url: string; headers: Record<string, string>; body: Record<string, unknown> } {
    const url = `${this.baseUrl}/data/query/${encodeURIComponent(this.state.table)}`;
    const headers = this.buildHeaders();
    return { url, headers, body: this.buildBody() };
  }

  protected buildHeaders(): Record<string, string> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...this.defaultHeaders,
    };

    if (this.sessionToken) {
      headers["Authorization"] = `Bearer ${this.sessionToken}`;
    } else if (this.apiKey) {
      headers["Authorization"] = `Bearer service.${this.apiKey}`;
    }

    if (this.database) {
      headers["Database"] = this.database;
    }

    // Build Prefer header based on operation
    const preferParts: string[] = [];

    switch (this.state.operation) {
      case "select":
        preferParts.push("operation=select");
        if (this.state.count) preferParts.push("count=exact");
        break;
      case "insert":
        preferParts.push("operation=insert");
        if (this.state.onConflict === "ignore") preferParts.push("on-conflict=ignore");
        break;
      case "upsert":
        preferParts.push("operation=insert", "on-conflict=replace");
        break;
      case "update":
        preferParts.push("operation=update");
        break;
      case "delete":
        preferParts.push("operation=delete");
        break;
    }

    if (preferParts.length > 0) {
      headers["Prefer"] = preferParts.join(",");
    }

    return headers;
  }
}
