"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { LoaderCircle } from "lucide-react";
import {
  getStoredPendingInvite,
  createBaseClient,
  extractMagicLinkToken,
  clearMagicLinkTokenFromURL,
  setStoredSessionToken,
} from "@/lib/atomicbase";

export default function AuthCallbackPage() {
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function handleCallback() {
      const token = extractMagicLinkToken();
      if (!token) {
        setError("No magic link token found in URL.");
        return;
      }

      const client = createBaseClient();
      const result = await client.auth.completeMagicLink(token);
      clearMagicLinkTokenFromURL();

      if (result.error) {
        setError(result.error.message);
        return;
      }

      setStoredSessionToken(result.data.token);
      const pendingInvite = getStoredPendingInvite();
      if (pendingInvite) {
        router.push("/invite");
        return;
      }
      router.push("/");
    }

    handleCallback();
  }, [router]);

  if (error) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="w-full max-w-sm rounded-lg border border-destructive/50 bg-destructive/10 p-6 text-center">
          <p className="text-sm font-medium text-destructive">Authentication failed</p>
          <p className="mt-2 text-sm text-muted-foreground">{error}</p>
          <a
            href="/"
            className="mt-4 inline-block text-sm font-medium text-primary underline-offset-4 hover:underline"
          >
            Back to login
          </a>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="flex flex-col items-center gap-3">
        <LoaderCircle className="h-8 w-8 animate-spin text-muted-foreground" />
        <p className="text-sm text-muted-foreground">Signing you in...</p>
      </div>
    </div>
  );
}
