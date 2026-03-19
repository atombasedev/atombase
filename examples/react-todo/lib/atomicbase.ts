"use client";

import { createClient, type AtomicbaseClient } from "@atomicbase/sdk";

export const SESSION_STORAGE_KEY = "atomicbase.react-todo.session";
export const ACTIVE_ORG_STORAGE_KEY = "atomicbase.react-todo.active-org";
export const PENDING_INVITE_STORAGE_KEY = "atomicbase.react-todo.pending-invite";
export const ORG_DEFINITION_NAME = process.env.NEXT_PUBLIC_ATOMICBASE_ORG_DEFINITION || "todo-team";

export function getAtomicbaseURL(): string {
  return process.env.NEXT_PUBLIC_ATOMICBASE_URL?.trim() || "http://localhost:8080";
}

export function createBaseClient(): AtomicbaseClient {
  return createClient({
    url: getAtomicbaseURL(),
  });
}

export function getStoredSessionToken(): string | null {
  if (typeof window === "undefined") {
    return null;
  }
  return window.localStorage.getItem(SESSION_STORAGE_KEY);
}

export function setStoredSessionToken(token: string): void {
  window.localStorage.setItem(SESSION_STORAGE_KEY, token);
}

export function clearStoredSessionToken(): void {
  window.localStorage.removeItem(SESSION_STORAGE_KEY);
}

export function getStoredActiveOrg(): string | null {
  if (typeof window === "undefined") {
    return null;
  }
  return window.localStorage.getItem(ACTIVE_ORG_STORAGE_KEY);
}

export function setStoredActiveOrg(orgId: string): void {
  window.localStorage.setItem(ACTIVE_ORG_STORAGE_KEY, orgId);
}

export function clearStoredActiveOrg(): void {
  window.localStorage.removeItem(ACTIVE_ORG_STORAGE_KEY);
}

export type PendingInvite = {
  orgId: string;
  inviteId: string;
};

export function getStoredPendingInvite(): PendingInvite | null {
  if (typeof window === "undefined") {
    return null;
  }
  const raw = window.localStorage.getItem(PENDING_INVITE_STORAGE_KEY);
  if (!raw) {
    return null;
  }
  try {
    return JSON.parse(raw) as PendingInvite;
  } catch {
    return null;
  }
}

export function setStoredPendingInvite(invite: PendingInvite): void {
  window.localStorage.setItem(PENDING_INVITE_STORAGE_KEY, JSON.stringify(invite));
}

export function clearStoredPendingInvite(): void {
  window.localStorage.removeItem(PENDING_INVITE_STORAGE_KEY);
}

export function extractMagicLinkToken(): string | null {
  if (typeof window === "undefined") {
    return null;
  }
  return new URL(window.location.href).searchParams.get("token");
}

export function clearMagicLinkTokenFromURL(): void {
  if (typeof window === "undefined") {
    return;
  }
  const url = new URL(window.location.href);
  url.searchParams.delete("token");
  window.history.replaceState({}, "", url.toString());
}
