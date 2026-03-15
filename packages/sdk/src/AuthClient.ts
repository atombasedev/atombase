import { AtomicbaseError } from "./AtomicbaseError.js";
import type {
  AtomicbaseResponse,
  CreateOrganizationMemberOptions,
  Organization,
  OrganizationMember,
  TransferOrganizationOwnershipOptions,
  UpdateOrganizationMemberOptions,
  UpdateOrganizationOptions,
} from "./types.js";

export class OrganizationAuthClient {
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

  private async request<T>(method: string, path: string, body?: unknown): Promise<AtomicbaseResponse<T>> {
    try {
      const response = await this.fetchFn(`${this.baseUrl}${path}`, {
        method,
        headers: this.getHeaders(),
        body: body ? JSON.stringify(body) : undefined,
      });

      if (!response.ok) {
        const errorBody = await response.json().catch(() => ({}));
        const error = AtomicbaseError.fromResponse(errorBody, response.status);
        return { data: null, error };
      }

      if (response.status === 204) {
        return { data: undefined as T, error: null };
      }

      const data = await response.json();
      return { data, error: null };
    } catch (err) {
      return { data: null, error: AtomicbaseError.networkError(err) };
    }
  }

  listMembers(orgId: string): Promise<AtomicbaseResponse<OrganizationMember[]>> {
    return this.request("GET", `/auth/orgs/${encodeURIComponent(orgId)}/members`);
  }

  addMember(orgId: string, options: CreateOrganizationMemberOptions): Promise<AtomicbaseResponse<OrganizationMember>> {
    return this.request("POST", `/auth/orgs/${encodeURIComponent(orgId)}/members`, options);
  }

  updateMember(orgId: string, userId: string, options: UpdateOrganizationMemberOptions): Promise<AtomicbaseResponse<OrganizationMember>> {
    return this.request("PATCH", `/auth/orgs/${encodeURIComponent(orgId)}/members/${encodeURIComponent(userId)}`, options);
  }

  removeMember(orgId: string, userId: string): Promise<AtomicbaseResponse<void>> {
    return this.request("DELETE", `/auth/orgs/${encodeURIComponent(orgId)}/members/${encodeURIComponent(userId)}`);
  }

  update(orgId: string, options: UpdateOrganizationOptions): Promise<AtomicbaseResponse<Organization>> {
    return this.request("PATCH", `/auth/orgs/${encodeURIComponent(orgId)}`, options);
  }

  delete(orgId: string): Promise<AtomicbaseResponse<void>> {
    return this.request("DELETE", `/auth/orgs/${encodeURIComponent(orgId)}`);
  }

  transferOwnership(orgId: string, options: TransferOrganizationOwnershipOptions): Promise<AtomicbaseResponse<Organization>> {
    return this.request("POST", `/auth/orgs/${encodeURIComponent(orgId)}/transfer-ownership`, options);
  }
}
