import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { AppService, ServiceService, errText, errKind } from "@/lib/api";
import { hasUpdateCheckBinding, hasUpdateApplyBinding, hasReloadGrantsBinding, callCheckManifest, callApplyUpdate, callReloadGrants, type UpdateCheckResult } from "@/lib/bindings";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { ErrorState } from "@/components/ui/error-state";
import { toast } from "sonner";

// About / Updates (M7). Versions come from live bindings, never the
// hardcoded sidebar string: AppService.Version is the GUI build
// (stamped at build time, "dev" in unstamped builds), Capabilities
// carries the daemon's app_version + control protocol, and
// ServiceService.Status carries the installed service state.
//
// Updates are daemon self-updates, NOT a separate GUI updater: there
// is no update feed and no automatic check (gui/updater.go records
// exactly that). An operator stages a signed manifest + binary, the
// screen verifies it with CheckManifest (ed25519 + SHA-256, downgrade
// rejected by the daemon with 409, never reported as "no update"),
// Apply launches the detached apply, and the screen polls
// CheckManifest's LastUpdate for the outcome — applied, rolled back,
// or error with the backup path — and never claims success from the
// apply response alone. Check() is deliberately NOT called: its
// binding exists but it cannot answer without a manifest path and
// errors truthfully saying so.
// Binding gate: CheckManifest/Apply/ReloadGrants are not in the
// checked-in bindings yet (see lib/bindings.ts). Until a Wails build
// regenerates them, the update controls are disabled with the exact
// reason and the version/service sections still work.
export function About() {
  const version = useQuery({ queryKey: ["app-version"], queryFn: () => AppService.Version() });
  const platform = useQuery({ queryKey: ["app-platform"], queryFn: () => AppService.Platform() });
  const caps = useQuery({ queryKey: ["capabilities"], queryFn: () => AppService.Capabilities() });
  const svc = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), refetchInterval: 5_000 });

  const [manifestPath, setManifestPath] = useState("");
  const [stagedBinaryPath, setStagedBinaryPath] = useState("");
  const [confirmApply, setConfirmApply] = useState(false);
  const [lastCheck, setLastCheck] = useState<UpdateCheckResult | null>(null);
  const [polling, setPolling] = useState(false);
  const [appliedAt, setAppliedAt] = useState(0);

  const canCheck = hasUpdateCheckBinding();
  const canApply = hasUpdateApplyBinding();
  const canReloadGrants = hasReloadGrantsBinding();

  const check = useMutation({
    mutationFn: () => callCheckManifest(manifestPath),
    onSuccess: (r) => { setLastCheck(r); toast.success(r.update_available ? `Update available: ${r.version} (running ${r.current_version})` : "No update in this manifest — running version is current or newer"); },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)}`),
  });
  const apply = useMutation({
    mutationFn: () => callApplyUpdate(manifestPath, stagedBinaryPath),
    onSuccess: (r) => { toast.info(r.detached ? `Detached apply launched for ${r.version} — polling for the outcome; do not assume success yet.` : `Apply returned without a detached process for ${r.version} — polling for the outcome.`); setAppliedAt(Date.now()); setPolling(true); },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)}`),
  });
  const reloadGrants = useMutation({
    mutationFn: () => callReloadGrants(),
    onSuccess: () => toast.success("Daemon grants reloaded — running roles now match the grant file, no restart needed"),
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)}`),
  });

  // Poll after apply until LastUpdate moves past the apply moment.
  useEffect(() => {
    if (!polling || !canCheck || !manifestPath) return;
    const t = setInterval(() => check.mutate(), 3_000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [polling, canCheck]);
  useEffect(() => {
    if (polling && lastCheck?.last_update && lastCheck.last_update.at_ms >= appliedAt - 5_000) {
      setPolling(false);
      const lu = lastCheck.last_update;
      if (lu.applied) toast.success(`Update applied: ${lu.version ?? lastCheck.version}`);
      else if (lu.rolled_back) toast.error(`Update rolled back${lu.error ? `: ${lu.error}` : ""}${lu.backup ? ` — backup at ${lu.backup}` : ""}`);
      else if (lu.error) toast.error(`Update failed: ${lu.error}${lu.backup ? ` — backup at ${lu.backup}` : ""}`);
    }
  }, [lastCheck, polling, appliedAt]);

  const lu = lastCheck?.last_update;
  return (
    <div className="space-y-4">
      <div><h1 className="text-2xl font-medium tracking-tight">About</h1><p className="text-sm text-[var(--text-muted)]">Build, daemon, service, and updates — from live queries, not hardcoded strings.</p></div>
      <Card>
        <CardHeader><CardTitle>Build</CardTitle></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          <div><div className="mono-label">GUI version</div><div className="mt-1 font-mono text-xs">{version.data ?? (version.isError ? `unavailable (${errText(version.error)})` : "…")}</div></div>
          <div><div className="mono-label">Platform</div><div className="mt-1 font-mono text-xs">{platform.data ?? (platform.isError ? `unavailable (${errText(platform.error)})` : "…")}</div></div>
          <div><div className="mono-label">Daemon version</div><div className="mt-1 font-mono text-xs">{caps.data?.app_version ?? (caps.isError ? `unavailable (${errText(caps.error)})` : "…")}</div></div>
          <div><div className="mono-label">Control protocol</div><div className="mt-1 font-mono text-xs">{caps.data ? `v${caps.data.protocol}` : "…"}</div></div>
          {caps.data?.features && <div className="sm:col-span-2"><div className="mono-label">Daemon features</div><div className="mt-1 flex flex-wrap gap-1">{Object.entries(caps.data.features).map(([k, v]) => <Badge key={k} tone={v ? "success" : "neutral"}>{k}{v ? "" : " (off)"}</Badge>)}</div></div>}
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>Service</CardTitle></CardHeader>
        <CardContent className="space-y-2 text-sm">
          <div className="flex flex-wrap items-center gap-3"><Badge tone={svc.data?.state === "running" ? "success" : "neutral"}>{svc.data?.state ?? "unknown"}</Badge><span className="text-[var(--text-muted)]">{svc.data?.manager ?? ""}</span><span className="text-[var(--text-muted)]">{svc.data?.installed ? "installed" : "not installed"}</span></div>
          {svc.data?.last_error && <p className="text-xs text-[var(--text-muted)]">Last error: {svc.data.last_error}</p>}
          <p className="text-xs text-[var(--text-faint)]">Install, uninstall, and repair live in Settings → Service, where every elevation is explained before it is requested.</p>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>Updates — daemon self-update</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs text-[var(--text-muted)]">
            Skein does not check a feed or update itself in the background. An operator stages a signed manifest and binary (from the release flow), this screen verifies the manifest against the daemon
            (ed25519 signature + SHA-256; a downgrade is rejected, never hidden), and Apply launches the daemon&apos;s detached apply process. The outcome is only known when a later check reports it:
            applied, rolled back with the error and backup path, or still in progress. If an apply fails, the previous binary and config backup are kept — never deleted on failure.
          </p>
          {(!canCheck || !canApply) && (
            <div className="rounded-md border border-[var(--border)] bg-[var(--surface-raised)] p-3 text-xs text-[var(--text-muted)]">
              Update check/apply are not callable in this build: the CheckManifest{!canApply ? " and Apply" : ""} Wails bindings have not been regenerated since Step 8.
              Run a Wails build on a GUI machine to regenerate bindings; these controls then activate with no further code change. The version and service sections above already work.
              Until then, apply updates from an elevated terminal with the daemon&apos;s signed-manifest flow, not from this screen.
            </div>
          )}
          <div><Label>Staged manifest path</Label><Input value={manifestPath} onChange={(e) => setManifestPath(e.target.value)} placeholder="/path/to/manifest.json" /></div>
          <div><Label>Staged binary path (optional — daemon defaults to the manifest staging dir)</Label><Input value={stagedBinaryPath} onChange={(e) => setStagedBinaryPath(e.target.value)} placeholder="/path/to/skeind" /></div>
          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" disabled={!canCheck || !manifestPath || check.isPending} onClick={() => check.mutate()}>Check manifest</Button>
            <Button variant="secondary" disabled={!canApply || !manifestPath || apply.isPending} onClick={() => setConfirmApply(true)}>Apply update</Button>
            <Dialog open={confirmApply} onOpenChange={setConfirmApply}>
              <DialogContent>
                <DialogTitle>Apply daemon update?</DialogTitle>
                <DialogDescription>
                  The privileged service binary will be swapped by the daemon&apos;s detached apply process from this verified manifest, and rolled back on failure with the previous binary and config backup kept.
                  Manifest: <code className="font-mono text-xs">{manifestPath}</code>
                </DialogDescription>
                <div className="mt-4 flex justify-end gap-2"><Button variant="secondary" onClick={() => { setConfirmApply(false); apply.mutate(); }} disabled={apply.isPending}>Apply update</Button></div>
              </DialogContent>
            </Dialog>
            <Button variant="secondary" disabled={!canReloadGrants || reloadGrants.isPending} onClick={() => reloadGrants.mutate()}>Reload grants</Button>
          </div>
          {check.isError && <ErrorState title="Check failed" message={`${errKind(check.error) ? errKind(check.error) + ": " : ""}${errText(check.error)}`} onRetry={() => check.mutate()} />}
          {lastCheck && (
            <div className="space-y-1 text-sm">
              <div>Manifest version: <span className="font-mono text-xs">{lastCheck.version}</span> · running: <span className="font-mono text-xs">{lastCheck.current_version}</span> · {lastCheck.update_available ? "update available" : "no update in this manifest"}</div>
              {polling && <div className="text-xs text-[var(--text-muted)]">Apply in progress — polling the daemon for the outcome…</div>}
              {lu && <div className="text-xs text-[var(--text-muted)]">Last update: {lu.version ?? "—"} · {lu.applied ? "applied" : lu.rolled_back ? "rolled back" : lu.error ? "failed" : "unknown"}{lu.error ? ` · ${lu.error}` : ""}{lu.backup ? ` · backup: ${lu.backup}` : ""}</div>}
            </div>
          )}
          <p className="text-xs text-[var(--text-faint)]">Trust note: applying an update replaces the privileged service binary, so it only ever runs from a manifest whose signature and hash the daemon itself verified, and only after you confirm here. The GUI never downloads or swaps a binary on its own.</p>
        </CardContent>
      </Card>
    </div>
  );
}
