"use client";

import { useEffect, useMemo, useState, useTransition } from "react";
import { Check, ChevronDown, LoaderCircle, LogOut, Mail, Plus, Trash2, Users } from "lucide-react";
import { toast } from "sonner";
import { eq } from "@atomicbase/sdk";
import type { AtomicbaseClient, Organization, OrganizationInvite, OrganizationMember, User } from "@atomicbase/sdk";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import {
  ORG_DEFINITION_NAME,
  clearStoredActiveOrg,
  clearStoredSessionToken,
  createBaseClient,
  getStoredActiveOrg,
  getStoredSessionToken,
  setStoredActiveOrg,
} from "@/lib/atomicbase";

type Todo = {
  id: string;
  title: string;
  completed: number;
  created_at: string;
  updated_at: string;
};

type AuthState =
  | { kind: "booting" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; user: User; token: string }
  | { kind: "error"; message: string };

export function TodoApp() {
  const baseClient = useMemo(() => createBaseClient(), []);
  const [authState, setAuthState] = useState<AuthState>({ kind: "booting" });
  const [todos, setTodos] = useState<Todo[]>([]);
  const [organizations, setOrganizations] = useState<Organization[]>([]);
  const [members, setMembers] = useState<OrganizationMember[]>([]);
  const [invites, setInvites] = useState<OrganizationInvite[]>([]);
  const [activeOrgId, setActiveOrgId] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [newTodoTitle, setNewTodoTitle] = useState("");
  const [newOrgName, setNewOrgName] = useState("");
  const [inviteEmail, setInviteEmail] = useState("");
  const [magicLinkSent, setMagicLinkSent] = useState(false);
  const [pending, startTransition] = useTransition();

  useEffect(() => {
    void bootstrap(baseClient);
  }, [baseClient]);

  async function bootstrap(client: AtomicbaseClient) {
    const stored = getStoredSessionToken();
    if (!stored) {
      setAuthState({ kind: "anonymous" });
      return;
    }

    const sessionClient = client.withSession(stored);
    const me = await sessionClient.auth.me();
    if (me.error) {
      clearStoredSessionToken();
      clearStoredActiveOrg();
      setAuthState({ kind: "anonymous" });
      return;
    }

    setAuthState({ kind: "authenticated", user: me.data, token: stored });
    await hydrateOrganizations(sessionClient, me.data, stored);
  }

  async function hydrateOrganizations(client: AtomicbaseClient, user: User, token: string) {
    const orgResult = await client.orgs.list();
    if (orgResult.error) {
      setAuthState({ kind: "error", message: orgResult.error.message });
      return;
    }

    const nextOrganizations = orgResult.data ?? [];
    setOrganizations(nextOrganizations);

    const storedOrg = getStoredActiveOrg();
    const fallbackOrg = nextOrganizations[0]?.id ?? null;
    const nextActiveOrg =
      storedOrg && nextOrganizations.some((org) => org.id === storedOrg)
        ? storedOrg
        : fallbackOrg;

    if (!nextActiveOrg) {
      setActiveOrgId(null);
      setMembers([]);
      setInvites([]);
      setTodos([]);
      setAuthState({ kind: "authenticated", user, token });
      return;
    }

    setActiveOrgId(nextActiveOrg);
    setStoredActiveOrg(nextActiveOrg);
    setAuthState({ kind: "authenticated", user, token });
    await loadOrganizationWorkspace(client, nextActiveOrg);
  }

  async function loadOrganizationWorkspace(client: AtomicbaseClient, orgId: string) {
    const [todosResult, membersResult, invitesResult] = await Promise.all([
      client.database(`org:${orgId}`).from<Todo>("todos").select("*").orderBy("created_at", "desc"),
      client.orgs.listMembers(orgId),
      client.orgs.listInvites(orgId),
    ]);

    if (todosResult.error) {
      toast.error("Failed to load tasks");
      return;
    }
    if (membersResult.error) {
      toast.error("Failed to load team members");
      return;
    }
    if (invitesResult.error) {
      toast.error("Failed to load invites");
      return;
    }

    setTodos(todosResult.data ?? []);
    setMembers(membersResult.data ?? []);
    setInvites(invitesResult.data ?? []);
  }

  async function handleMagicLinkStart(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setMagicLinkSent(false);
    startTransition(async () => {
      const result = await baseClient.auth.startMagicLink({ email });
      if (result.error) {
        toast.error(result.error.message);
        return;
      }
      setMagicLinkSent(true);
      toast.success("Check your email for a sign-in link");
    });
  }

  async function handleCreateOrganization(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (authState.kind !== "authenticated") return;

    const name = newOrgName.trim();
    if (!name) return;

    startTransition(async () => {
      const client = baseClient.withSession(authState.token);
      const created = await client.orgs.create({
        id: crypto.randomUUID(),
        name,
        definition: ORG_DEFINITION_NAME,
      });
      if (created.error) {
        toast.error(created.error.message);
        return;
      }
      setNewOrgName("");
      toast.success(`Created "${created.data.name}"`);
      await hydrateOrganizations(client, authState.user, authState.token);
    });
  }

  async function handleOrganizationChange(orgId: string) {
    if (authState.kind !== "authenticated") return;
    setActiveOrgId(orgId);
    setStoredActiveOrg(orgId);
    startTransition(async () => {
      const client = baseClient.withSession(authState.token);
      await loadOrganizationWorkspace(client, orgId);
    });
  }

  async function handleAddTodo(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (authState.kind !== "authenticated" || !activeOrgId) return;

    const title = newTodoTitle.trim();
    if (!title) return;

    startTransition(async () => {
      const client = baseClient.withSession(authState.token);
      const result = await client.database(`org:${activeOrgId}`).from<Todo>("todos").insert({
        id: crypto.randomUUID(),
        title,
        completed: 0,
        updated_at: new Date().toISOString(),
      });
      if (result.error) {
        toast.error(result.error.message);
        return;
      }
      setNewTodoTitle("");
      await loadOrganizationWorkspace(client, activeOrgId);
    });
  }

  async function handleToggle(todo: Todo) {
    if (authState.kind !== "authenticated" || !activeOrgId) return;

    startTransition(async () => {
      const client = baseClient.withSession(authState.token);
      const result = await client
        .database(`org:${activeOrgId}`)
        .from<Todo>("todos")
        .update({
          completed: todo.completed ? 0 : 1,
          updated_at: new Date().toISOString(),
        })
        .where(eq("id", todo.id));
      if (result.error) {
        toast.error(result.error.message);
        return;
      }
      await loadOrganizationWorkspace(client, activeOrgId);
    });
  }

  async function handleDelete(todoId: string) {
    if (authState.kind !== "authenticated" || !activeOrgId) return;

    startTransition(async () => {
      const client = baseClient.withSession(authState.token);
      const result = await client.database(`org:${activeOrgId}`).from<Todo>("todos").delete().where(eq("id", todoId));
      if (result.error) {
        toast.error(result.error.message);
        return;
      }
      await loadOrganizationWorkspace(client, activeOrgId);
    });
  }

  async function handleInvite(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (authState.kind !== "authenticated" || !activeOrgId) return;
    const normalized = inviteEmail.trim();
    if (!normalized) return;

    startTransition(async () => {
      const client = baseClient.withSession(authState.token);
      const invite = await client.orgs.createInvite(activeOrgId, {
        email: normalized,
        role: "member",
      });
      if (invite.error) {
        toast.error(invite.error.message);
        return;
      }
      setInviteEmail("");
      toast.success(`Invite sent to ${invite.data.email}`);
      await loadOrganizationWorkspace(client, activeOrgId);
    });
  }

  async function handleSignOut() {
    if (authState.kind !== "authenticated") return;

    startTransition(async () => {
      const client = baseClient.withSession(authState.token);
      await client.auth.signOut();
      clearStoredSessionToken();
      clearStoredActiveOrg();
      setTodos([]);
      setOrganizations([]);
      setMembers([]);
      setInvites([]);
      setActiveOrgId(null);
      setAuthState({ kind: "anonymous" });
    });
  }

  const activeOrganization = organizations.find((org) => org.id === activeOrgId) ?? null;
  const completedCount = todos.filter((todo) => todo.completed).length;
  const currentMemberRole =
    authState.kind === "authenticated"
      ? members.find((member) => member.userId === authState.user.id)?.role ?? null
      : null;
  const canInvite = currentMemberRole === "owner";

  if (authState.kind === "booting") {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <LoaderCircle className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (authState.kind === "error") {
    return (
      <div className="flex min-h-screen items-center justify-center p-4">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-destructive">Error</CardTitle>
            <CardDescription>{authState.message}</CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              className="w-full"
              onClick={() => {
                clearStoredSessionToken();
                clearStoredActiveOrg();
                setAuthState({ kind: "anonymous" });
              }}
            >
              Try again
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (authState.kind === "anonymous") {
    return (
      <div className="flex min-h-screen items-center justify-center p-4">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle>Sign in</CardTitle>
            <CardDescription>Enter your email to receive a magic link</CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleMagicLinkStart} className="space-y-4">
              <Input
                id="email"
                type="email"
                autoComplete="email"
                placeholder="you@example.com"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
              />
              <Button
                className={`w-full transition-all duration-300 ${magicLinkSent ? "bg-green-600 hover:bg-green-600 cursor-default" : ""}`}
                type="submit"
                disabled={pending || magicLinkSent}
              >
                {magicLinkSent ? (
                  <Check className="mr-2 h-4 w-4 animate-in zoom-in duration-200" />
                ) : pending ? (
                  <LoaderCircle className="mr-2 h-4 w-4 animate-spin" />
                ) : (
                  <Mail className="mr-2 h-4 w-4" />
                )}
                {magicLinkSent ? "Link sent" : "Send magic link"}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-muted/30">
      <header className="border-b bg-background">
        <div className="mx-auto flex max-w-2xl items-center justify-between gap-4 px-4 py-3">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" className="gap-2 font-semibold">
                <Check className="h-4 w-4 text-primary" />
                {activeOrganization?.name ?? "Select workspace"}
                <ChevronDown className="h-4 w-4 text-muted-foreground" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-64">
              {organizations.length > 0 && (
                <>
                  <DropdownMenuLabel>Workspaces</DropdownMenuLabel>
                  {organizations.map((org) => (
                    <DropdownMenuItem
                      key={org.id}
                      onClick={() => void handleOrganizationChange(org.id)}
                      className={activeOrgId === org.id ? "bg-accent" : ""}
                    >
                      {org.name}
                      {activeOrgId === org.id && <Check className="ml-auto h-4 w-4" />}
                    </DropdownMenuItem>
                  ))}
                  <DropdownMenuSeparator />
                </>
              )}
              <DropdownMenuLabel>Create new</DropdownMenuLabel>
              <form onSubmit={handleCreateOrganization} className="px-2 pb-2">
                <div className="flex gap-2">
                  <Input
                    placeholder="Workspace name"
                    value={newOrgName}
                    onChange={(event) => setNewOrgName(event.target.value)}
                    className="h-8 text-sm"
                    onClick={(e) => e.stopPropagation()}
                    onKeyDown={(e) => e.stopPropagation()}
                  />
                  <Button type="submit" size="sm" disabled={pending || !newOrgName.trim()}>
                    <Plus className="h-4 w-4" />
                  </Button>
                </div>
              </form>
            </DropdownMenuContent>
          </DropdownMenu>

          <div className="flex items-center gap-2">
            {activeOrgId && (
              <Sheet>
                <SheetTrigger asChild>
                  <Button variant="outline" size="sm">
                    <Users className="h-4 w-4" />
                    <span className="ml-2">Team</span>
                  </Button>
                </SheetTrigger>
                <SheetContent className="px-6">
                  <SheetHeader>
                    <SheetTitle>Team</SheetTitle>
                    <SheetDescription>
                      Manage members and invites for {activeOrganization?.name}
                    </SheetDescription>
                  </SheetHeader>
                  <div className="mt-6 space-y-6">
                    <div>
                      <h3 className="mb-3 text-sm font-medium">Members</h3>
                      {members.length === 0 ? (
                        <p className="text-sm text-muted-foreground">No members yet.</p>
                      ) : (
                        <div className="space-y-2">
                          {members.map((member) => (
                            <div
                              key={member.userId}
                              className="flex items-center justify-between rounded-lg border bg-background px-3 py-2 text-sm"
                            >
                              <span className="truncate">{member.email}</span>
                              <span className="ml-2 shrink-0 capitalize text-muted-foreground">{member.role}</span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>

                    {canInvite && (
                      <div>
                        <h3 className="mb-3 text-sm font-medium">Invite someone</h3>
                        <form onSubmit={handleInvite} className="flex gap-2">
                          <Input
                            type="email"
                            placeholder="Email address"
                            value={inviteEmail}
                            onChange={(event) => setInviteEmail(event.target.value)}
                            className="flex-1"
                          />
                          <Button type="submit" disabled={pending || !inviteEmail.trim()}>
                            Invite
                          </Button>
                        </form>
                      </div>
                    )}

                    {invites.length > 0 && (
                      <div>
                        <h3 className="mb-3 text-sm font-medium">Pending invites</h3>
                        <div className="space-y-2">
                          {invites.map((invite) => (
                            <div
                              key={invite.id}
                              className="flex items-center justify-between rounded-lg border bg-background px-3 py-2 text-sm"
                            >
                              <span className="truncate">{invite.email}</span>
                              <span className="ml-2 shrink-0 text-muted-foreground">Pending</span>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                </SheetContent>
              </Sheet>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="sm" className="gap-2">
                  <span className="hidden text-sm text-muted-foreground sm:inline">{authState.user.email}</span>
                  <LogOut className="h-4 w-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuLabel>{authState.user.email}</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => void handleSignOut()} disabled={pending}>
                  <LogOut className="mr-2 h-4 w-4" />
                  Sign out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-2xl px-4 py-6">
        {organizations.length === 0 ? (
          <Card>
            <CardHeader className="text-center">
              <CardTitle>Welcome! Create your first workspace</CardTitle>
              <CardDescription>
                Workspaces let you organize tasks and collaborate with others.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form onSubmit={handleCreateOrganization} className="flex gap-2">
                <Input
                  placeholder="e.g. My Team, Personal, Work"
                  value={newOrgName}
                  onChange={(event) => setNewOrgName(event.target.value)}
                  className="flex-1"
                  autoFocus
                />
                <Button type="submit" disabled={pending || !newOrgName.trim()}>
                  {pending ? (
                    <LoaderCircle className="mr-2 h-4 w-4 animate-spin" />
                  ) : (
                    <Plus className="mr-2 h-4 w-4" />
                  )}
                  Create
                </Button>
              </form>
            </CardContent>
          </Card>
        ) : !activeOrgId ? (
          <Card>
            <CardContent className="py-12 text-center">
              <p className="text-muted-foreground">No workspace selected</p>
              <p className="mt-1 text-sm text-muted-foreground">
                Select a workspace using the dropdown above.
              </p>
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-4">
            <form onSubmit={handleAddTodo} className="flex gap-2">
              <Input
                placeholder="What needs to be done?"
                value={newTodoTitle}
                onChange={(event) => setNewTodoTitle(event.target.value)}
                className="flex-1"
              />
              <Button type="submit" disabled={pending || !newTodoTitle.trim()}>
                <Plus className="mr-2 h-4 w-4" />
                Add
              </Button>
            </form>

            {todos.length === 0 ? (
              <Card>
                <CardContent className="py-12 text-center">
                  <p className="text-muted-foreground">No tasks yet</p>
                  <p className="mt-1 text-sm text-muted-foreground">Add your first task above.</p>
                </CardContent>
              </Card>
            ) : (
              <>
                <div className="flex items-center justify-between text-sm text-muted-foreground">
                  <span>
                    {todos.length} {todos.length === 1 ? "task" : "tasks"}
                  </span>
                  <span>{completedCount} done</span>
                </div>
                <div className="space-y-2">
                  {todos.map((todo) => (
                    <div
                      key={todo.id}
                      className="group flex items-center gap-3 rounded-lg border bg-background p-3 transition-colors hover:bg-muted/50"
                    >
                      <Checkbox
                        checked={!!todo.completed}
                        onCheckedChange={() => void handleToggle(todo)}
                        className="h-5 w-5"
                      />
                      <span className={`flex-1 ${todo.completed ? "text-muted-foreground line-through" : ""}`}>
                        {todo.title}
                      </span>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => void handleDelete(todo.id)}
                        className="opacity-0 transition-opacity group-hover:opacity-100"
                      >
                        <Trash2 className="h-4 w-4 text-muted-foreground" />
                      </Button>
                    </div>
                  ))}
                </div>
              </>
            )}
          </div>
        )}
      </main>
    </div>
  );
}

