import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, ShieldAlert, ShieldCheck, Wrench } from "lucide-react";
import { toast } from "sonner";
import { DiagnosticsService, FirewallService, InviteService, errDisplay, errKind, errText, type Invite } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { CopyField } from "@/components/ui/copy-field";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { ErrorState } from "@/components/ui/error-state";
import { AccessPanel } from "@/features/security/AccessPanel";
import { IdentityPanel } from "@/features/security/IdentityPanel";

export function Security() {
  const qc = useQueryClient();
  // Local state only: the firewall is re-read on demand and after a repair,
  // and once a minute while the window is visible so an out-of-band change
  // still shows up without polling everything.
  const fw = useQuery({ queryKey: ["firewall"], queryFn: () => FirewallService.Status(), refetchInterval: 30_000 });
  const sec = useQuery({ queryKey: ["security"], queryFn: () => DiagnosticsService.Security() });

  const repair = useMutation({
    mutationFn: () => FirewallService.Repair(),
    onSuccess: (r) => {
      if (r.last_error) toast.error(`Repair finished with an error: ${r.last_error}`);
      else toast.success("Managed firewall rules repaired");
      void qc.invalidateQueries({ queryKey: ["firewall"] });
    },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errDisplay(e)}`),
  });

  const [confirmRotate, setConfirmRotate] = useState(false);
  const [rotated, setRotated] = useState<Invite | null>(null);
  const rotate = useMutation({
    mutationFn: () => InviteService.RotateSecret(),
    onSuccess: (invite) => {
      setConfirmRotate(false);
      // The value is secret-bearing: it stays in this component's memory and
      // is never put in a toast, a notification, history or a URL.
      setRotated(invite);
      toast.success("Network secret rotated. Every peer must rejoin with the new join string.");
      void qc.invalidateQueries({ queryKey: ["identity"] });
      void qc.invalidateQueries({ queryKey: ["security"] });
      void qc.invalidateQueries({ queryKey: ["firewall"] });
    },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errDisplay(e)}`),
  });

  if (fw.isError) {
    return (
      <div className="space-y-4">
        <Header />
        <ErrorState
          title="Cannot read the firewall"
          message={errText(fw.error)}
          remediation={errKind(fw.error) === "Daemon offline" ? "The daemon is not reachable. Start the Skein service, then reopen this screen." : undefined}
          onRetry={() => fw.refetch()}
        />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <Header />

      <AccessPanel />
      <IdentityPanel />

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><RefreshCw size={16} className="text-[var(--danger)]" /> Rotate the network secret</CardTitle>
          <Badge tone="danger">network-wide</Badge>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs leading-relaxed text-[var(--text-muted)]">
            Rotation is the only network-wide revocation this release has. It replaces the credential every member shares, so every peer loses access until it rejoins with the new join string. It is not per-peer revocation, and it does not remove anyone from the mesh by itself.
          </p>
          <p className="text-xs leading-relaxed text-[var(--text-muted)]">
            The daemon backs up the old configuration, writes the new secret and restarts the service. The new join string is returned once, here, and is never stored by this app.
          </p>
          <Button variant="danger" onClick={() => setConfirmRotate(true)} disabled={rotate.isPending}>
            <RefreshCw size={16} /> Rotate network secret
          </Button>
          {rotated && (
            <div className="space-y-2">
              <div className="flex gap-2 rounded-md border border-[var(--warning)]/40 bg-[var(--warning)]/5 p-3 text-xs text-[var(--warning)]">
                <ShieldAlert size={16} className="shrink-0" />
                <span>{rotated.warning || "Every peer must rejoin with this join string. It will not be shown again after you dismiss it."}</span>
              </div>
              {rotated.value ? (
                <CopyField value={rotated.value} label="new network join string (secret — share once, then dismiss)" secret />
              ) : (
                <div className="rounded-md border border-[var(--danger)]/40 p-3 text-xs text-[var(--danger)]">
                  The secret was rotated but the daemon returned no join string. Treat the network as rotated and unreachable until a new join string is produced; do not reuse the old one.
                </div>
              )}
              <Button variant="secondary" size="sm" onClick={() => setRotated(null)}>Dismiss (the join string will not be shown again)</Button>
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Firewall</CardTitle>
          <Badge tone={fw.data?.state === "ok" ? "success" : fw.data?.state === "error" ? "danger" : fw.data?.state === "degraded" ? "warning" : "neutral"}>{fw.data?.state ?? "loading"}</Badge>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-3">
            <div><div className="mono-label">manager</div><div className="mt-1 text-sm">{fw.data?.manager || "not detected"}</div></div>
            <div><div className="mono-label">rules</div><div className="mt-1 text-sm">{fw.data?.rules?.length ?? 0} managed</div></div>
            <div><div className="mono-label">last check</div><div className="mt-1 text-sm">{fw.data?.last_check_ms ? new Date(fw.data.last_check_ms).toLocaleTimeString() : "—"}</div></div>
          </div>
          {fw.data?.rules && fw.data.rules.length > 0 && (
            <div className="overflow-x-auto rounded-md border border-[var(--border)]">
              <table className="w-full min-w-[560px] text-left text-xs" aria-label="Managed firewall rules">
                <thead className="border-b border-[var(--border)] text-[var(--text-faint)]">
                  <tr>{["Name", "Direction", "Action", "Protocol", "Port", "Interface"].map((h) => <th key={h} className="mono-label px-3 py-2 font-normal">{h}</th>)}</tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {fw.data.rules.map((r) => (
                    <tr key={r.name}>
                      <td className="px-3 py-2 font-mono">{r.name}</td>
                      <td className="px-3 py-2">{r.direction}</td>
                      <td className="px-3 py-2">{r.action}</td>
                      <td className="px-3 py-2">{r.protocol}</td>
                      <td className="px-3 py-2">{r.port || "—"}</td>
                      <td className="px-3 py-2">{r.interface || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {fw.data?.last_error && <div className="rounded-md border border-[var(--danger)]/40 p-3 text-xs text-[var(--danger)]">{fw.data.last_error}</div>}
          <Button variant="secondary" onClick={() => repair.mutate()} disabled={repair.isPending}>
            <Wrench size={15} /> {repair.isPending ? "Repairing…" : "Repair rules"}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><ShieldCheck size={16} className="text-[var(--accent)]" /> Local permission checks</CardTitle>
          {sec.isFetching && <Badge tone="neutral">refreshing</Badge>}
        </CardHeader>
        <CardContent className="space-y-3">
          {sec.isError && <ErrorState title="Cannot read the local checks" message={errText(sec.error)} onRetry={() => sec.refetch()} />}
          <div className="grid gap-3 sm:grid-cols-2">
            {[
              ["config owner", sec.data?.config_owner], ["config mode", sec.data?.config_mode],
              ["identity owner", sec.data?.identity_owner], ["identity mode", sec.data?.identity_mode],
              ["control socket", sec.data?.socket_path], ["update signature", sec.data?.update_signature],
            ].map(([k, v]) => <div key={k}><div className="mono-label">{k}</div><div className="mt-1 truncate font-mono text-xs">{v || "—"}</div></div>)}
          </div>
          <p className="text-xs text-[var(--text-muted)]">Read-only facts about the files the daemon owns. Nothing on this screen changes them.</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>Arbitrary OS rules</CardTitle></CardHeader>
        <CardContent>
          <p className="text-sm text-[var(--text-muted)]">Skein exposes only the rules it manages. It never exposes a shell or an arbitrary firewall-rule editor.</p>
        </CardContent>
      </Card>

      <Dialog open={confirmRotate} onOpenChange={(v) => { if (!rotate.isPending) setConfirmRotate(v); }}>
        <DialogContent>
          <DialogTitle>Rotate the network secret?</DialogTitle>
          <DialogDescription>
            Every peer must rejoin. The credential every member shares is replaced, so all peers lose access until they join with the new join string; this is the only network-wide revocation in this release. The daemon backs up the old configuration and restarts the service. The new join string is shown once, here, and this app does not store it.
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setConfirmRotate(false)} disabled={rotate.isPending}>Cancel</Button>
            <Button variant="danger" onClick={() => rotate.mutate()} disabled={rotate.isPending}>
              {rotate.isPending ? "Rotating…" : "Rotate and restart"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function Header() {
  return (
    <div>
      <h1 className="text-2xl font-medium tracking-tight">Security</h1>
      <p className="text-sm text-[var(--text-muted)]">Identity, effective access, network secret rotation and the rules Skein manages.</p>
    </div>
  );
}
