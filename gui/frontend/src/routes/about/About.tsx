import { useCallback, useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AppService, NetworkService, ServiceService, UpdateService, errKind, errText } from "@/lib/api";
import type { UpdateCheckResult, UpdateState } from "../../../bindings/github.com/Utsav-X-bit/skein/internal/api/models";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { ErrorState } from "@/components/ui/error-state";
import { Input, Label } from "@/components/ui/input";
import { toast } from "sonner";

const PollIntervalMs = 2_000;
const PollAttemptsMax = 90;

/**
 * About / Updates. Versions come from live bindings, never a hardcoded string:
 * AppService.Version is the GUI build, Capabilities carries the daemon's
 * app_version and control protocol, ServiceService.Status carries the
 * installed service state and binary version, and NetworkService.Status carries
 * the version the running daemon actually reports.
 *
 * Updates are daemon self-updates, not a GUI updater: there is no feed and no
 * automatic check. An operator stages a signed manifest and binary, Check
 * verifies them through the daemon (ed25519 + SHA-256; a downgrade is rejected
 * with 409, never reported as "no update"), Apply launches the detached apply
 * after a confirmation, and this screen polls CheckManifest's LastUpdate for
 * the outcome. It then reads Status().Version and reports what is actually
 * running, because "applied" is the outcome file's claim, not proof that the
 * new binary is serving requests.
 */
export function About() {
  const qc = useQueryClient();
  const version = useQuery({ queryKey: ["app-version"], queryFn: () => AppService.Version() });
  const platform = useQuery({ queryKey: ["app-platform"], queryFn: () => AppService.Platform() });
  const caps = useQuery({ queryKey: ["capabilities"], queryFn: () => AppService.Capabilities() });
  // AppShell owns the service interval; this is an observer of the same key.
  const service = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), retry: 0 });

  const [manifestPath, setManifestPath] = useState("");
  const [binaryPath, setBinaryPath] = useState("");
  const [checked, setChecked] = useState<{ path: string; result: UpdateCheckResult } | null>(null);
  const [confirmApply, setConfirmApply] = useState(false);
  const [applying, setApplying] = useState(false);
  const [appliedAt, setAppliedAt] = useState(0);
  // The path the apply actually ran with; polling must not follow a later edit
  // of the manifest input.
  const [appliedPath, setAppliedPath] = useState("");
  const [outcome, setOutcome] = useState<UpdateState | null>(null);
  const [pollNote, setPollNote] = useState("");
  const [running, setRunning] = useState<{ version: string; at: number } | null>(null);
  const [runningNote, setRunningNote] = useState("");
  const [runningBusy, setRunningBusy] = useState(false);

  const stagedPath = manifestPath.trim();
  const verified = checked && checked.path === stagedPath ? checked.result : null;

  // Status().Version is the running daemon's own report; it is unreadable while
  // the daemon restarts, which is a fact worth showing rather than guessing.
  const readRunning = useCallback(async () => {
    setRunningBusy(true);
    try {
      const status = await NetworkService.Status();
      setRunning({ version: status.version, at: Date.now() });
      setRunningNote("");
    } catch (e) {
      setRunningNote(errText(e));
    } finally {
      setRunningBusy(false);
    }
  }, []);

  const check = useMutation({
    mutationFn: () => UpdateService.CheckManifest(stagedPath),
    onSuccess: (result) => {
      setChecked({ path: stagedPath, result });
      toast.success(
        result.update_available
          ? `Verified ${result.version}; running ${result.current_version}.`
          : `Verified ${result.version}; the running ${result.current_version} is current or newer.`,
      );
    },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)}`),
  });

  const apply = useMutation({
    mutationFn: () => UpdateService.Apply(stagedPath, binaryPath.trim()),
    onSuccess: (result) => {
      toast.info(
        result.detached
          ? `Detached apply launched for ${result.version}. The outcome is not known yet; this screen polls for it.`
          : `Apply returned without a detached process for ${result.version}. Polling for the outcome.`,
      );
      setOutcome(null);
      setPollNote("");
      setAppliedPath(checked?.path ?? stagedPath);
      setAppliedAt(Date.now());
      setApplying(true);
    },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)}`),
  });

  // Poll until the apply writes an outcome newer than the apply moment, then
  // read the running version. The poll is silent: no toast per attempt.
  useEffect(() => {
    if (!applying || !appliedPath) return;
    let cancelled = false;
    let timer = 0;
    let attempts = 0;
    const tick = async () => {
      attempts += 1;
      try {
        const result = await UpdateService.CheckManifest(appliedPath);
        if (cancelled) return;
        setPollNote("");
        const last = result.last_update;
        if (last && last.at_ms >= appliedAt - 5_000 && (last.applied || last.rolled_back || Boolean(last.error))) {
          setOutcome(last);
          setApplying(false);
          void qc.invalidateQueries({ queryKey: ["status"] });
          void qc.invalidateQueries({ queryKey: ["service"] });
          void readRunning();
          return;
        }
      } catch (e) {
        if (!cancelled) setPollNote(`Poll failed: ${errText(e)}`);
      }
      if (cancelled) return;
      if (attempts >= PollAttemptsMax) {
        setApplying(false);
        setPollNote("No outcome after 3 minutes. The apply may still be running; check again when the service is back.");
        return;
      }
      timer = window.setTimeout(() => void tick(), PollIntervalMs);
    };
    timer = window.setTimeout(() => void tick(), 1_000);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [applying, appliedPath, appliedAt, qc, readRunning]);

  useEffect(() => {
    if (!outcome) return;
    if (outcome.applied) toast.success(`Update applied: ${outcome.version ?? "version not reported"}`);
    else if (outcome.rolled_back) {
      toast.error(`Update rolled back${outcome.error ? `: ${outcome.error}` : ""}${outcome.backup ? ` — backup at ${outcome.backup}` : ""}`);
    } else if (outcome.error) {
      toast.error(`Update failed: ${outcome.error}${outcome.backup ? ` — backup at ${outcome.backup}` : ""}`);
    }
  }, [outcome]);

  const canApply = Boolean(verified?.update_available) && !applying && !apply.isPending;
  const versionMismatch = Boolean(outcome?.applied && outcome.version && running && running.version !== outcome.version);

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-medium tracking-tight">About</h1>
        <p className="text-sm text-[var(--text-muted)]">Build, daemon, service and updates — read from the running system, not hardcoded strings.</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Build</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          <div>
            <div className="mono-label">GUI version</div>
            <div className="mt-1 font-mono text-xs">{version.data ?? (version.isError ? `unavailable (${errText(version.error)})` : "…")}</div>
          </div>
          <div>
            <div className="mono-label">Platform</div>
            <div className="mt-1 font-mono text-xs">{platform.data ?? (platform.isError ? `unavailable (${errText(platform.error)})` : "…")}</div>
          </div>
          <div>
            <div className="mono-label">Daemon version</div>
            <div className="mt-1 font-mono text-xs">{caps.data?.app_version ?? (caps.isError ? `unavailable (${errText(caps.error)})` : "…")}</div>
          </div>
          <div>
            <div className="mono-label">Control protocol</div>
            <div className="mt-1 font-mono text-xs">{caps.data ? `v${caps.data.protocol}` : caps.isError ? `unavailable (${errText(caps.error)})` : "…"}</div>
          </div>
          <div className="sm:col-span-2">
            <div className="mono-label">Capabilities</div>
            {caps.data?.features ? (
              <div className="mt-1 flex flex-wrap gap-1">
                {Object.entries(caps.data.features).map(([name, on]) => (
                  <Badge key={name} tone={on ? "success" : "neutral"}>
                    {name}
                    {on ? "" : " (off)"}
                  </Badge>
                ))}
              </div>
            ) : (
              <div className="mt-1 text-xs text-[var(--text-muted)]">{caps.isError ? "not reported" : "…"}</div>
            )}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Service</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <div className="flex flex-wrap items-center gap-3">
            <Badge tone={service.data?.state === "running" ? "success" : service.data?.state === "error" ? "danger" : "neutral"}>
              {service.data?.state ?? (service.isError ? "unavailable" : "…")}
            </Badge>
            <span className="text-[var(--text-muted)]">{service.data?.manager ?? ""}</span>
            <span className="text-[var(--text-muted)]">{service.data ? (service.data.installed ? "installed" : "not installed") : ""}</span>
            <span className="font-mono text-xs text-[var(--text-muted)]">{service.data?.version ? `binary ${service.data.version}` : "binary version not reported"}</span>
          </div>
          {service.data?.last_error && <p className="text-xs text-[var(--text-muted)]">Last error: {service.data.last_error}</p>}
          {service.isError && <p className="text-xs text-[var(--danger)]">{errText(service.error)}</p>}
          <p className="text-xs text-[var(--text-faint)]">Install, uninstall and repair live in Settings → Service, where every elevation is explained before it is requested.</p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Updates — daemon self-update</CardTitle>
          <Badge tone="neutral">no feed · no automatic check</Badge>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs leading-relaxed text-[var(--text-muted)]">
            Skein does not check a feed or update itself in the background. An operator stages a signed manifest and binary from the release flow; Check has the daemon verify them
            (ed25519 signature and SHA-256, with a downgrade rejected rather than hidden); Apply launches the daemon&apos;s detached apply process after you confirm. The outcome is only
            known when a later check reports it — applied, rolled back with the error and backup path, or still running. A failed apply keeps the previous binary and configuration backup.
          </p>

          <div>
            <Label htmlFor="update-manifest">Staged manifest path</Label>
            <Input
              id="update-manifest"
              value={manifestPath}
              onChange={(e) => setManifestPath(e.target.value)}
              placeholder="/path/to/manifest.json"
              spellCheck={false}
            />
          </div>
          <div>
            <Label htmlFor="update-binary">Staged binary path (optional — the daemon defaults to the manifest staging directory)</Label>
            <Input
              id="update-binary"
              value={binaryPath}
              onChange={(e) => setBinaryPath(e.target.value)}
              placeholder="/path/to/skeind"
              spellCheck={false}
            />
          </div>

          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" disabled={!stagedPath || check.isPending} onClick={() => check.mutate()}>
              {check.isPending ? "Checking…" : "Check manifest"}
            </Button>
            <Button variant="secondary" disabled={!canApply} onClick={() => setConfirmApply(true)}>
              Apply update
            </Button>
            <Dialog open={confirmApply} onOpenChange={setConfirmApply}>
              <DialogContent>
                <DialogTitle>Apply daemon update?</DialogTitle>
                <DialogDescription>
                  The privileged service binary will be swapped by the daemon&apos;s detached apply process from this verified manifest, and rolled back on failure with the previous
                  binary and configuration backup kept. Manifest: <code className="font-mono text-xs">{stagedPath}</code>
                  {verified ? ` · verified ${verified.version}` : ""}
                </DialogDescription>
                <div className="mt-4 flex justify-end gap-2">
                  <Button variant="ghost" onClick={() => setConfirmApply(false)}>
                    Cancel
                  </Button>
                  <Button
                    disabled={apply.isPending}
                    onClick={() => {
                      setConfirmApply(false);
                      apply.mutate();
                    }}
                  >
                    {apply.isPending ? "Applying…" : "Apply update"}
                  </Button>
                </div>
              </DialogContent>
            </Dialog>
          </div>

          {stagedPath && !verified && (
            <p className="text-xs text-[var(--text-faint)]">Check this manifest before applying; the daemon verifies its signature and hash first, and Apply stays disabled until it does.</p>
          )}
          {check.isError && (
            <ErrorState
              title="Check failed"
              message={`${errKind(check.error) ? errKind(check.error) + ": " : ""}${errText(check.error)}`}
              onRetry={() => check.mutate()}
            />
          )}

          {verified && (
            <div className="space-y-2 rounded-md border border-[var(--border)] p-3">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge tone={verified.update_available ? "info" : "neutral"}>{verified.update_available ? "update available" : "no newer version"}</Badge>
                <span>
                  verified manifest <span className="font-mono text-xs">{verified.version}</span>
                </span>
                <span className="text-xs text-[var(--text-muted)]">running {verified.current_version}</span>
              </div>
              <div>
                <div className="mono-label">release notes (signed)</div>
                {verified.release_notes ? (
                  <pre className="mt-1 whitespace-pre-wrap break-words font-sans text-xs leading-relaxed text-[var(--text-muted)]">{verified.release_notes}</pre>
                ) : (
                  <p className="mt-1 text-xs text-[var(--text-faint)]">This manifest carries no signed release notes.</p>
                )}
              </div>
              {verified.last_update && <LastUpdate state={verified.last_update} />}
            </div>
          )}

          {applying && (
            <p className="text-xs text-[var(--text-muted)]" role="status">
              Apply in progress — polling the daemon for the outcome. Do not assume success until it reports one.
            </p>
          )}
          {pollNote && <p className="text-xs text-[var(--text-muted)]">{pollNote}</p>}

          {outcome && (
            <div className="space-y-1 rounded-md border border-[var(--border)] p-3 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <Badge tone={outcome.applied ? "success" : "danger"}>{outcome.applied ? "applied" : outcome.rolled_back ? "rolled back" : "failed"}</Badge>
                <span className="font-mono">{outcome.version ?? "version not reported"}</span>
                <span className="text-[var(--text-muted)]">{new Date(outcome.at_ms).toLocaleString()}</span>
              </div>
              {outcome.error && <p className="break-words text-[var(--danger)]">{outcome.error}</p>}
              {outcome.backup && (
                <p className="text-[var(--text-muted)]">
                  Backup kept at <span className="break-all font-mono text-[var(--text)]">{outcome.backup}</span>
                </p>
              )}
            </div>
          )}

          <div className="space-y-1 rounded-md border border-[var(--border)] p-3 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <span className="mono-label">running daemon</span>
              <span className="font-mono text-[var(--text)]">{running ? running.version : "not read"}</span>
              <Button size="sm" variant="ghost" disabled={runningBusy} onClick={() => void readRunning()}>
                {runningBusy ? "Reading…" : "Read version"}
              </Button>
            </div>
            {running && <p className="text-[var(--text-muted)]">Read {new Date(running.at).toLocaleTimeString()} from the daemon&apos;s own status.</p>}
            {runningNote && <p className="text-[var(--danger)]">Status is unreadable: {runningNote}. The daemon may still be restarting.</p>}
            {versionMismatch && (
              <p className="text-[var(--warning)]">
                The daemon reports {running?.version}, not the applied {outcome?.version}. The outcome file says applied; the running process has not reported it yet.
              </p>
            )}
            {outcome?.applied && outcome.version && running?.version === outcome.version && (
              <p className="text-[var(--success)]">The running daemon reports the applied version.</p>
            )}
          </div>

          <p className="text-xs text-[var(--text-faint)]">
            Trust note: applying an update replaces the privileged service binary, so it only runs from a manifest whose signature and hash the daemon itself verified, and only after you
            confirm here. The GUI never downloads or swaps a binary on its own.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

function LastUpdate({ state }: { state: UpdateState }) {
  const status = state.applied ? "applied" : state.rolled_back ? "rolled back" : state.error ? "failed" : "no outcome yet";
  return (
    <div className="border-t border-[var(--border)] pt-2 text-xs text-[var(--text-muted)]">
      Last update: <span className="font-mono text-[var(--text)]">{state.version ?? "—"}</span> · {status}
      {state.error ? ` · ${state.error}` : ""}
      {state.backup ? ` · backup: ${state.backup}` : ""}
    </div>
  );
}
