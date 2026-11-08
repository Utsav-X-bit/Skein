import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, ShieldAlert, ShieldCheck, Trash2, UserCog } from "lucide-react";
import { toast } from "sonner";
import { AppService, SecurityService, errDisplay, errKind, errText } from "@/lib/api";
import type { RevokeOutcome } from "../../../bindings/github.com/Utsav-X-bit/skein/internal/app/models";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { Input, Label } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { consequenceText, revokeArg, revokeConsequence, Roles, type RevokeConsequence, type Role } from "./model";

export const GrantsKey = ["security", "grants"];

const RoleTones: Record<string, "neutral" | "accent" | "danger"> = { read: "neutral", mutate: "accent", secret: "danger" };

const roleHelp: Record<string, string> = {
  read: "may read status, peers and telemetry",
  mutate: "may also change config and manage peers",
  secret: "may also read this table, grant and revoke, and rotate the secret",
};

function errorLine(e: unknown): string {
  return `${errKind(e) ? errKind(e) + ": " : ""}${errDisplay(e)}`;
}

/**
 * AccessPanel reads the daemon's live authorisation table over the kernel
 * socket and runs the fixed helper verbs. The GUI holds no privilege itself:
 * every write is one helper invocation with a typed argument, and the table
 * is re-read afterwards so the screen shows what the daemon actually holds.
 */
export function AccessPanel() {
  const qc = useQueryClient();
  const grants = useQuery({ queryKey: GrantsKey, queryFn: () => SecurityService.Grants(), staleTime: 5_000 });
  const platform = useQuery({ queryKey: ["app-platform"], queryFn: () => AppService.Platform() });
  const isWindows = platform.data === "windows";
  const [grantOpen, setGrantOpen] = useState(false);
  const [uid, setUid] = useState("");
  const [role, setRole] = useState<Role>("mutate");
  const grant = useMutation({
    mutationFn: () => SecurityService.GrantUser(Number(uid), role),
    onSuccess: () => {
      toast.success(`Granted uid ${uid} the ${role} role`);
      setGrantOpen(false);
      setUid("");
    },
    // A reload failure means the grant is already written: report the error
    // and still re-read, so the table never hides a persisted change.
    onError: (e) => toast.error(errorLine(e)),
    onSettled: () => void qc.invalidateQueries({ queryKey: GrantsKey }),
  });

  const [revokeOpen, setRevokeOpen] = useState(false);
  const [revokeFor, setRevokeFor] = useState<string | null>(null);
  const [revokeInput, setRevokeInput] = useState("");
  const [force, setForce] = useState<RevokeConsequence | null>(null);
  const [outcome, setOutcome] = useState<RevokeOutcome | null>(null);
  const revoke = useMutation({
    mutationFn: ({ identity, force }: { identity: string; force: boolean }) =>
      SecurityService.RevokeUser(revokeArg(identity), force),
    onSuccess: (r) => {
      setForce(null);
      setOutcome(r);
      setRevokeFor(null);
      setRevokeInput("");
      setRevokeOpen(false);
      toast.success(r.Role ? `Revoked the ${r.Role} grant for ${r.Identity}` : `Revoked the grant for ${r.Identity}`);
    },
    onError: (e) => {
      const c = revokeConsequence(e);
      if (c) {
        setForce(c);
        return;
      }
      toast.error(errorLine(e));
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: GrantsKey }),
  });
  const revokeIdentity = revokeFor ?? revokeInput;
  const closeRevoke = () => {
    if (revoke.isPending) return;
    setRevokeOpen(false);
    setRevokeFor(null);
    setRevokeInput("");
    setForce(null);
  };

  const [ownerOpen, setOwnerOpen] = useState(false);
  const [owner, setOwnerName] = useState("");
  const setOwner = useMutation({
    mutationFn: () => SecurityService.SetOwner(owner.trim()),
    onSuccess: () => {
      toast.success(`Ownership transferred to ${owner.trim()}. Restart the Skein service to apply it.`);
      setOwnerOpen(false);
      setOwnerName("");
    },
    onError: (e) => toast.error(errorLine(e)),
  });

  const caller = grants.data?.caller;
  const rows = grants.data?.grants ?? [];

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><ShieldCheck size={16} className="text-[var(--accent)]" /> This caller</CardTitle>
          {caller && <Badge tone={RoleTones[caller.role] ?? "neutral"}>{caller.role}</Badge>}
        </CardHeader>
        <CardContent className="space-y-3">
          {grants.isLoading && <Skeleton className="h-10 w-full" />}
          {grants.isError && (
            <ErrorState
              title="Cannot read the grant table"
              message={errText(grants.error)}
              remediation={
                errKind(grants.error) === "Daemon offline"
                  ? "The daemon is not reachable. Start the Skein service, then reopen this screen."
                  : /404|not found/i.test(errText(grants.error))
                    ? "This daemon build does not expose the grants endpoint. Update the daemon to the matching release, then reopen this screen. The running version is shown in About."
                    : "The table is secret-gated: it needs the secret role over the local kernel socket. A bearer (development TCP) connection is capped at read and can never see it. Ask an administrator to grant this user the secret role, then reopen this screen."
              }
              onRetry={() => grants.refetch()}
            />
          )}
          {caller && (
            <>
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="mono-label">identity</span>
                <code className="rounded border border-[var(--border)] bg-[var(--surface)] px-1.5 py-0.5 font-mono text-[11px]">{caller.id}</code>
                <span className="mono-label ml-2">transport</span>
                <Badge tone={caller.transport === "kernel" ? "success" : "warning"}>{caller.transport}</Badge>
                {caller.is_root && <Badge tone="warning">implicit secret (root)</Badge>}
              </div>
              <p className="text-xs leading-relaxed text-[var(--text-muted)]">
                {caller.transport === "kernel"
                  ? "This connection arrives over the local control socket, so its identity is the operating-system user that started the app."
                  : "This connection arrives over a development TCP bearer token. A bearer caller is capped at read and cannot use this table; run the desktop app against the local control socket to manage access."}
                {" "}
                {roleHelp[caller.role] ? `A ${caller.role} caller ${roleHelp[caller.role]}.` : `The daemon reports the role “${caller.role}” for this caller.`}
              </p>
            </>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Effective grants</CardTitle>
          <div className="flex gap-2">
            {!isWindows && (
              <Button size="sm" onClick={() => setGrantOpen(true)} disabled={grants.isError}>
                <KeyRound size={14} /> Grant a user
              </Button>
            )}
            <Button size="sm" variant="secondary" onClick={() => { setRevokeFor(null); setRevokeInput(""); setForce(null); setOutcome(null); setRevokeOpen(true); }} disabled={grants.isError}>
              <Trash2 size={14} /> Revoke
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs text-[var(--text-muted)]">
            The table the daemon authorises against. Root and SYSTEM/Administrators hold implicit secret access and never appear as grant lines.
          </p>
          {grants.isLoading && <Skeleton className="h-24 w-full" />}
          {grants.isSuccess && rows.length === 0 && (
            <EmptyState
              icon={<UserCog size={20} />}
              title="No explicit grants"
              description="Every non-root caller is denied until a grant is added. Root/SYSTEM keep implicit secret access."
            />
          )}
          {rows.length > 0 && (
            <div className="overflow-x-auto rounded-md border border-[var(--border)]">
              <table className="w-full text-left text-sm" aria-label="Effective grants">
                <thead className="border-b border-[var(--border)] text-[var(--text-faint)]">
                  <tr>
                    {["Identity", "Role", "Actions"].map((h) => (
                      <th key={h} className="mono-label px-3 py-2 font-normal">{h === "Actions" ? <span className="sr-only">Actions</span> : h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {rows.map((g) => (
                    <tr key={g.identity} className="hover:bg-[var(--surface-raised)]">
                      <td className="px-3 py-2 font-mono text-xs">{g.identity}</td>
                      <td className="px-3 py-2"><Badge tone={RoleTones[g.role] ?? "neutral"}>{g.role || "read (bare line)"}</Badge></td>
                      <td className="px-3 py-2 text-right">
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Revoke ${g.identity}`}
                          onClick={() => { setOutcome(null); setForce(null); setRevokeInput(""); setRevokeFor(g.identity); setRevokeOpen(true); }}
                        >
                          <Trash2 size={14} /> Revoke
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {outcome && (
            <div className="space-y-2 rounded-md border border-[var(--border)] p-3 text-xs">
              <div className="flex items-center gap-2">
                <ShieldCheck size={14} className="text-[var(--success)]" />
                <span>Revoked {outcome.Role ? `the ${outcome.Role} grant for ` : "the grant for "}<code className="font-mono">{outcome.Identity}</code></span>
              </div>
              {outcome.Warning && <div className="flex gap-2 text-[var(--warning)]"><ShieldAlert size={14} className="shrink-0" />{outcome.Warning}</div>}
              <div className="text-[var(--text-muted)]">
                {outcome.Reloaded ? "The running daemon re-read the file; the change applies now." : outcome.ReloadNote || "The running daemon was not reloaded; the change applies at next start."}
              </div>
              <Button size="sm" variant="secondary" onClick={() => setOutcome(null)}>Dismiss</Button>
            </div>
          )}
        </CardContent>
      </Card>

      {isWindows && (
        <Card>
          <CardHeader>
            <CardTitle>Ownership</CardTitle>
            <Badge tone="info">windows</Badge>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-xs leading-relaxed text-[var(--text-muted)]">
              On Windows the control pipe and system config belong to one account. Set owner transfers both; the new owner's grant and pipe access are built when the daemon starts, so restart the Skein service afterwards. Per-SID grants are read-only here.
            </p>
            <Button size="sm" variant="secondary" onClick={() => setOwnerOpen(true)}><UserCog size={14} /> Set owner</Button>
          </CardContent>
        </Card>
      )}

      <Dialog open={grantOpen} onOpenChange={(v) => { if (!grant.isPending) setGrantOpen(v); }}>
        <DialogContent>
          <DialogTitle>Grant a user</DialogTitle>
          <DialogDescription>
            Adds an explicit grant line for a local user id and reloads the running daemon. The helper asks for administrator approval (polkit) before it writes.
          </DialogDescription>
          <div className="mt-4 space-y-3">
            <div>
              <Label htmlFor="grant-uid">User id (uid)</Label>
              <Input id="grant-uid" inputMode="numeric" value={uid} onChange={(e) => setUid(e.target.value.replace(/[^0-9]/g, ""))} placeholder="1000" />
            </div>
            <div>
              <Label htmlFor="grant-role">Role</Label>
              <select
                id="grant-role"
                value={role}
                onChange={(e) => setRole(e.target.value as Role)}
                className="h-10 w-full rounded-md border border-[var(--border-strong)] bg-[var(--surface)] px-3 text-sm text-[var(--text)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
              >
                {Roles.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
              <p className="mt-2 text-xs text-[var(--text-muted)]">A {role} caller {roleHelp[role]}.</p>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="secondary" onClick={() => setGrantOpen(false)} disabled={grant.isPending}>Cancel</Button>
              <Button onClick={() => grant.mutate()} disabled={grant.isPending || !/^[1-9][0-9]*$/.test(uid)}>
                {grant.isPending ? "Granting…" : "Grant"}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={revokeOpen} onOpenChange={(v) => { if (!v) closeRevoke(); }}>
        <DialogContent>
          <DialogTitle>Revoke a grant</DialogTitle>
          <DialogDescription>
            Removes the grant line and reloads the running daemon, so the caller loses access on its next request. This is local to this node: it does not remove a peer or change the network secret.
          </DialogDescription>
          <div className="mt-4 space-y-3">
            {revokeFor === null && (
              <div>
                <Label htmlFor="revoke-id">Identity</Label>
                <Input id="revoke-id" value={revokeInput.trim()} onChange={(e) => { setRevokeInput(e.target.value); setForce(null); }} placeholder={isWindows ? "S-1-5-21-…" : "uid:1000 or 1000"} />
              </div>
            )}
            {revokeFor !== null && (
              <div className="text-sm">
                <span className="mono-label">identity</span>
                <code className="ml-2 rounded border border-[var(--border)] bg-[var(--surface)] px-1.5 py-0.5 font-mono text-xs">{revokeFor}</code>
              </div>
            )}
            {force && (
              <div className="space-y-2 rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs">
                <div className="flex gap-2 text-[var(--danger)]"><ShieldAlert size={16} className="shrink-0" /><strong>The helper refused this revoke.</strong></div>
                <p className="text-[var(--text)]">{consequenceText(force)}</p>
                <p className="text-[var(--text-muted)]">Revoke anyway? The file is kept, never deleted, so a reload cannot turn “no grants” back into unconfigured read access.</p>
              </div>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="secondary" onClick={closeRevoke} disabled={revoke.isPending}>Cancel</Button>
              {force ? (
                <Button variant="danger" onClick={() => revoke.mutate({ identity: revokeIdentity, force: true })} disabled={revoke.isPending}>
                  {revoke.isPending ? "Revoking…" : "Revoke anyway (force)"}
                </Button>
              ) : (
                <Button
                  variant="danger"
                  onClick={() => revoke.mutate({ identity: revokeIdentity, force: false })}
                  disabled={revoke.isPending || !revokeIdentity.trim()}
                >
                  {revoke.isPending ? "Revoking…" : "Revoke"}
                </Button>
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={ownerOpen} onOpenChange={(v) => { if (!setOwner.isPending) setOwnerOpen(v); }}>
        <DialogContent>
          <DialogTitle>Set owner</DialogTitle>
          <DialogDescription>
            Transfers the system config and control pipe to another account through the elevated helper. The new owner's grant and pipe access are built when the daemon starts, so restart the Skein service to apply the change.
          </DialogDescription>
          <div className="mt-4 space-y-3">
            <div>
              <Label htmlFor="owner-user">Windows account</Label>
              <Input id="owner-user" value={owner} onChange={(e) => setOwnerName(e.target.value)} placeholder="DOMAIN\user" />
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="secondary" onClick={() => setOwnerOpen(false)} disabled={setOwner.isPending}>Cancel</Button>
              <Button onClick={() => setOwner.mutate()} disabled={setOwner.isPending || !owner.trim()}>
                {setOwner.isPending ? "Transferring…" : "Transfer ownership"}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
