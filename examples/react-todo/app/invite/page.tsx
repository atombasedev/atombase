"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { LoaderCircle, Mail, UserPlus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  clearStoredPendingInvite,
  clearStoredSessionToken,
  createBaseClient,
  getStoredPendingInvite,
  getStoredSessionToken,
  setStoredActiveOrg,
  setStoredPendingInvite,
} from "@/lib/atomicbase";

type InviteState =
  | { kind: "booting" }
  | { kind: "needsSignIn"; orgId: string; inviteId: string; message?: string }
  | { kind: "accepting"; orgId: string; inviteId: string }
  | { kind: "error"; message: string }
  | { kind: "accepted"; message: string };

export default function InvitePage() {
  return (
    <Suspense fallback={<InviteLoading message="Loading invite..." />}>
      <InvitePageContent />
    </Suspense>
  );
}

function InvitePageContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [email, setEmail] = useState("");
  const [state, setState] = useState<InviteState>({ kind: "booting" });

  useEffect(() => {
    const orgId = searchParams.get("org");
    const inviteId = searchParams.get("invite");
    if (orgId && inviteId) {
      setStoredPendingInvite({ orgId, inviteId });
      void resolveInvite(orgId, inviteId);
      return;
    }

    const stored = getStoredPendingInvite();
    if (!stored) {
      setState({ kind: "error", message: "Invite link is missing organization details." });
      return;
    }
    void resolveInvite(stored.orgId, stored.inviteId);
  }, [searchParams]);

  async function resolveInvite(orgId: string, inviteId: string) {
    const token = getStoredSessionToken();
    if (!token) {
      setState({ kind: "needsSignIn", orgId, inviteId });
      return;
    }

    const client = createBaseClient().withSession(token);
    const me = await client.auth.me();
    if (me.error) {
      clearStoredSessionToken();
      setState({ kind: "needsSignIn", orgId, inviteId, message: "Sign in to accept this invite." });
      return;
    }

    setState({ kind: "accepting", orgId, inviteId });
    const accepted = await client.orgs.acceptInvite(orgId, inviteId);
    if (accepted.error) {
      setState({ kind: "error", message: accepted.error.message });
      return;
    }

    setStoredActiveOrg(orgId);
    clearStoredPendingInvite();
    setState({ kind: "accepted", message: "Invite accepted. Redirecting to your workspace..." });
    router.replace("/");
  }

  async function handleMagicLinkStart(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (state.kind !== "needsSignIn") {
      return;
    }

    const result = await createBaseClient().auth.startMagicLink({ email });
    if (result.error) {
      setState({ kind: "needsSignIn", orgId: state.orgId, inviteId: state.inviteId, message: result.error.message });
      return;
    }
    setState({
      kind: "needsSignIn",
      orgId: state.orgId,
      inviteId: state.inviteId,
      message: "Check your email. After sign-in, the app will accept the invite automatically.",
    });
  }

  if (state.kind === "booting" || state.kind === "accepting") {
    return <InviteLoading message={state.kind === "accepting" ? "Accepting invite..." : "Loading invite..."} />;
  }

  if (state.kind === "error") {
    return (
      <div className="flex min-h-screen items-center justify-center p-4">
        <Card className="w-full max-w-sm">
          <CardHeader>
            <CardTitle>Invite error</CardTitle>
            <CardDescription>{state.message}</CardDescription>
          </CardHeader>
          <CardContent>
            <Button className="w-full" onClick={() => router.push("/")}>
              Go to app
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (state.kind === "accepted") {
    return <InviteLoading message={state.message} icon={<UserPlus className="h-8 w-8 text-muted-foreground" />} />;
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <CardTitle>Accept organization invite</CardTitle>
          <CardDescription>
            Sign in with the invited email address and the app will accept the invite automatically.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={handleMagicLinkStart}>
            <Input
              id="email"
              type="email"
              autoComplete="email"
              placeholder="you@example.com"
              required
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
            <Button className="w-full" type="submit">
              <Mail className="mr-2 h-4 w-4" />
              Send magic link
            </Button>
            {state.message ? <p className="text-center text-sm text-muted-foreground">{state.message}</p> : null}
          </form>
        </CardContent>
      </Card>
    </div>
  );
}

function InviteLoading({
  message,
  icon,
}: {
  message: string;
  icon?: React.ReactNode;
}) {
  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="flex flex-col items-center gap-3">
        {icon ?? <LoaderCircle className="h-8 w-8 animate-spin text-muted-foreground" />}
        <p className="text-sm text-muted-foreground">{message}</p>
      </div>
    </div>
  );
}
