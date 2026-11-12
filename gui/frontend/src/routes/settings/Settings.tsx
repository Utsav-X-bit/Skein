import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Save, Server } from "lucide-react";
import { toast } from "sonner";
import {
  ConfigService,
  ServiceService,
  errDisplay,
  errKind,
  type ConfigPatch,
  type ConfigPatchResult,
  type ConfigView,
} from "@/lib/api";
import { useUI, type Motion, type Theme } from "@/lib/store";
import { cn } from "@/lib/utils";
import { NetworkRecovery } from "./NetworkRecovery";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { ErrorState } from "@/components/ui/error-state";
import { Input, Label } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";

type NodeDraft = { name: string; port: string; mtu: string; mdns: boolean };
type ServiceAction = "start" | "stop" | "restart" | "install" | "repair" | "uninstall";
type ConfirmAction = "install" | "repair" | "uninstall";

const draftFrom = (c: ConfigView): NodeDraft => ({
  name: c.name ?? "",
  port: String(c.listen_port),
  mtu: String(c.mtu),
  mdns: c.mdns,
});

// Validate only what the form owns; the daemon is the authority for the rest.
function toPatch(draft: NodeDraft): { patch: ConfigPatch } | { error: string } {
  const port = Number(draft.port);
  const mtu = Number(draft.mtu);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    return { error: "Listen port must be a whole number between 1 and 65535." };
  }
  if (!Number.isInteger(mtu) || mtu < 576 || mtu > 65535) {
    return { error: "MTU must be a whole number between 576 and 65535." };
  }
  return { patch: { name: draft.name.trim(), listen_port: port, mtu, mdns: draft.mdns } };
}

const serviceTone = (state?: string) =>
  state === "running" ? "success" : state === "missing" || state === "error" ? "danger" : state ? "warning" : "neutral";

function Choice<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: ReadonlyArray<readonly [T, string]>;
  onChange: (value: T) => void;
}) {
  return (
    <div>
      <div className="mono-label mb-1.5">{label}</div>
      <div role="group" aria-label={label} className="inline-flex rounded-md border border-[var(--border-strong)] p-0.5">
        {options.map(([option, text]) => (
          <button
            key={option}
            type="button"
            aria-pressed={value === option}
            onClick={() => onChange(option)}
            className={cn(
              "rounded px-3 py-1.5 text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
              value === option ? "bg-[var(--accent-soft)] text-[var(--accent)]" : "text-[var(--text-muted)] hover:text-[var(--text)]",
            )}
          >
            {text}
          </button>
        ))}
      </div>
    </div>
  );
}

export function Settings() {
  const qc = useQueryClient();
  const theme = useUI((s) => s.theme);
  const motion = useUI((s) => s.motion);
  const setTheme = useUI((s) => s.setTheme);
  const setMotion = useUI((s) => s.setMotion);

  // Dirty-state protection: while the form has unsaved edits, incoming
  // snapshots are ignored and polling pauses, so a refetch can never clobber
  // what the user is typing. A successful apply resumes syncing.
  const [dirty, setDirty] = useState(false);
  const [draft, setDraft] = useState<NodeDraft | null>(null);
  const [patchError, setPatchError] = useState("");
  const [applied, setApplied] = useState<ConfigPatchResult | null>(null);
  const synced = useRef<ConfigView | null>(null);

  const cfg = useQuery({
    queryKey: ["config"],
    queryFn: () => ConfigService.Get(),
    refetchInterval: dirty ? false : 10_000,
  });

  useEffect(() => {
    if (!cfg.data || dirty || synced.current === cfg.data) return;
    synced.current = cfg.data;
    setDraft(draftFrom(cfg.data));
  }, [cfg.data, dirty]);

  const edit = (patch: Partial<NodeDraft>) => {
    setDraft((value) => (value ? { ...value, ...patch } : value));
    setDirty(true);
    setPatchError("");
    setApplied(null);
  };

  const discard = () => {
    if (cfg.data) {
      synced.current = cfg.data;
      setDraft(draftFrom(cfg.data));
    }
    setDirty(false);
    setPatchError("");
    setApplied(null);
  };

  const patch = useMutation({
    mutationFn: (next: ConfigPatch) => ConfigService.Patch(next),
    onSuccess: (result) => {
      setApplied(result);
      setPatchError("");
      setDirty(false);
      void cfg.refetch();
    },
    onError: (e) => setPatchError(errDisplay(e)),
  });

  const apply = () => {
    if (!draft) return;
    const next = toPatch(draft);
    if ("error" in next) {
      setPatchError(next.error);
      return;
    }
    patch.mutate(next.patch);
  };

  // One service mutation for every verb: each resolves only after the
  // ServiceService has re-read the authoritative OS state, so the status
  // returned here is what the card shows next.
  const [serviceError, setServiceError] = useState("");
  const [repairStep, setRepairStep] = useState("");
  const [confirmAction, setConfirmAction] = useState<ConfirmAction | "">("");

  const service = useMutation({
    mutationFn: async (action: ServiceAction) => {
      if (action === "start") await ServiceService.Start();
      else if (action === "stop") await ServiceService.Stop();
      else if (action === "restart") await ServiceService.Restart();
      else if (action === "install") await ServiceService.Install();
      else if (action === "uninstall") await ServiceService.Uninstall();
      else {
        setRepairStep("stopping");
        await ServiceService.Stop();
        setRepairStep("re-registering");
        await ServiceService.Install();
        setRepairStep("starting");
        await ServiceService.Start();
      }
      return ServiceService.Status();
    },
    onSuccess: (status, action) => {
      setServiceError("");
      // The mutation only resolves after ServiceService re-read the OS state,
      // so this status is authoritative; write it now rather than waiting for
      // the next poll.
      qc.setQueryData(["service"], status);
      if (action === "restart") setApplied((value) => (value ? { ...value, restart_required: false } : value));
      if (action === "uninstall") toast.success("Service removed. Configuration and identity were kept.");
      else if (action === "repair") toast.success(`Service repaired; it is now ${status.state}.`);
      else toast.success(`Service is now ${status.state}.`);
    },
    onError: (e) => {
      const text = errDisplay(e);
      setServiceError(text);
      toast.error(text);
    },
    onSettled: () => {
      setRepairStep("");
      void cfg.refetch();
    },
  });

  // Boot start is a policy, not a lifecycle state: the helper changes the
  // unit/SCM start type and ServiceService re-reads it before returning.
  const boot = useMutation({
    mutationFn: async (enabled: boolean) => {
      await ServiceService.SetStartAtBoot(enabled);
      return ServiceService.Status();
    },
    onSuccess: (status) => {
      qc.setQueryData(["service"], status);
      toast.success(status.enabled ? "The service will start at boot." : "The service starts when you open Skein Desktop.");
    },
    onError: (e) => toast.error(errDisplay(e)),
  });

  const svc = useQuery({
    queryKey: ["service"],
    queryFn: () => ServiceService.Status(),
    // AppShell already polls this key every 5 s; sharing the cache keeps one
    // timer for the whole app.
  });

  const busy = service.isPending;
  const c = cfg.data;
  const daemonOffline = errKind(cfg.error) === "Daemon offline";

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-medium tracking-tight">Settings</h1>
        <p className="text-sm text-[var(--text-muted)]">
          Node settings, service lifecycle and the effective configuration. Secrets stay in the root-owned config and are never shown here.
        </p>
      </div>

      <NetworkRecovery departed={c?.departed ?? false} configPath={c?.config_path} />

      {cfg.isLoading && !c && <Skeleton className="h-56" />}
      {cfg.isError && (
        <ErrorState
          title="Configuration unavailable"
          message={errDisplay(cfg.error)}
          remediation={
            daemonOffline
              ? "The daemon is not reachable. Start the service below, then retry; service controls work without the daemon's config."
              : "Service controls and network recovery remain available below."
          }
          onRetry={() => void cfg.refetch()}
        />
      )}

      {c && draft && (
        <Card>
          <CardHeader>
            <CardTitle>Node</CardTitle>
            <Badge tone="neutral">protocol v{c.protocol}</Badge>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <Label htmlFor="node-name">name</Label>
                <Input
                  id="node-name"
                  value={draft.name}
                  maxLength={32}
                  onChange={(e) => edit({ name: e.target.value })}
                  placeholder="studio"
                />
              </div>
              <div>
                <Label htmlFor="node-port">listen port</Label>
                <Input
                  id="node-port"
                  value={draft.port}
                  inputMode="numeric"
                  onChange={(e) => edit({ port: e.target.value })}
                />
              </div>
              <div>
                <Label htmlFor="node-mtu">MTU</Label>
                <Input
                  id="node-mtu"
                  value={draft.mtu}
                  inputMode="numeric"
                  onChange={(e) => edit({ mtu: e.target.value })}
                />
                <p className="mt-1 text-[10px] text-[var(--text-muted)]">
                  Floor; the daemon raises the live MTU automatically when every direct path supports it.
                </p>
              </div>
              <div className="flex items-end gap-3 pb-2">
                <Switch
                  id="node-mdns"
                  aria-label="local discovery (mDNS)"
                  checked={draft.mdns}
                  onCheckedChange={(value) => edit({ mdns: value })}
                />
                <Label htmlFor="node-mdns" className="mb-0">
                  local discovery (mDNS)
                </Label>
              </div>
            </div>

            <p className="text-xs leading-relaxed text-[var(--text-muted)]">
              The daemon writes this file; the app never edits it directly. Name, port, MTU and mDNS apply without a restart. Changing the interface, STUN or public address needs a service restart.
            </p>

            {patchError && (
              <div role="alert" className="rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs text-[var(--danger)]">
                {patchError}
              </div>
            )}

            {applied && (
              <div role="status" className="rounded-md border border-[var(--border)] bg-[var(--surface-raised)] p-3 text-xs">
                <div className="font-medium text-[var(--text)]">
                  {applied.applied && applied.applied.length > 0
                    ? `Applied: ${applied.applied.join(", ")}`
                    : "No field changed."}
                </div>
                {applied.restart_required ? (
                  <div className="mt-2 flex flex-wrap items-center gap-2 text-[var(--warning)]">
                    <span>Restart the service for the new settings to take effect.</span>
                    <Button size="sm" variant="secondary" disabled={busy} onClick={() => service.mutate("restart")}>
                      Restart service
                    </Button>
                  </div>
                ) : (
                  <div className="mt-1 text-[var(--text-muted)]">Live now; no restart needed.</div>
                )}
              </div>
            )}

            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs text-[var(--text-muted)]" role="status">
                {dirty ? "Unsaved changes — incoming snapshots will not overwrite them." : "In sync with the daemon."}
              </span>
              <div className="flex gap-2">
                <Button variant="ghost" disabled={!dirty || patch.isPending} onClick={discard}>
                  Discard
                </Button>
                <Button disabled={!dirty || patch.isPending} onClick={apply}>
                  <Save size={15} /> {patch.isPending ? "Applying…" : "Apply"}
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Server size={16} /> Service
          </CardTitle>
          <Badge tone={serviceTone(svc.data?.state)}>{svc.data?.state ?? "unknown"}</Badge>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--text-muted)]">
            <span>manager: {svc.data?.manager || "unknown"}</span>
            <span>installed: {svc.data ? (svc.data.installed ? "yes" : "no") : "unknown"}</span>
            {svc.data?.version && <span>version: {svc.data.version}</span>}
            {svc.isError && <span role="status">state unavailable: {errDisplay(svc.error)}</span>}
          </div>

          <div className="flex items-start justify-between gap-4 rounded-md border border-[var(--border)] bg-[var(--surface-raised)] p-3">
            <div>
              <div className="text-sm font-medium">Start at boot</div>
              <p className="mt-1 text-xs leading-relaxed text-[var(--text-muted)]">
                Off by default. The service starts when you open Skein Desktop; turn this on to keep the mesh up after a reboot without the app.
              </p>
            </div>
            <Switch
              checked={svc.data?.enabled ?? false}
              disabled={busy || boot.isPending || !svc.data?.installed}
              onCheckedChange={(value) => boot.mutate(value)}
              aria-label="Start the service at boot"
            />
          </div>

          <div className="rounded-md border border-[var(--border)] bg-[var(--surface-raised)] p-3 text-xs leading-relaxed text-[var(--text-muted)]">
            <span className="font-medium text-[var(--text)]">Before any elevation: </span>
            these controls use only the minimal helper&apos;s fixed verbs — the app never gets a shell and keeps no administrator access afterwards. Install re-registers and starts an existing daemon; first-run create or join registers a fresh service instead. Start, Stop and Restart change the service lifecycle. Repair stops, re-registers and starts, stopping at the first failed step. Uninstall removes the service and keeps configuration and identity. Every verb may ask for approval (pkexec on Linux, UAC on Windows); the real OS state is re-read after each one, including failures.
          </div>

          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" disabled={busy} onClick={() => service.mutate("start")}>
              Start
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => service.mutate("stop")}>
              Stop
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => service.mutate("restart")}>
              Restart
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => setConfirmAction("install")}>
              Install
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => setConfirmAction("repair")}>
              Repair
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => setConfirmAction("uninstall")}>
              Uninstall
            </Button>
          </div>

          {repairStep && <p className="text-xs text-[var(--text-muted)]" role="status">Repair: {repairStep}…</p>}

          {serviceError && (
            <div role="alert" className="flex flex-wrap items-center gap-3 rounded-md border border-[var(--danger)]/40 bg-[var(--danger)]/5 p-3 text-xs text-[var(--danger)]">
              <AlertTriangle size={15} className="shrink-0" />
              <span className="min-w-0 flex-1">{serviceError}</span>
              {service.variables && (
                <Button size="sm" variant="secondary" disabled={busy} onClick={() => service.mutate(service.variables as ServiceAction)}>
                  Try again
                </Button>
              )}
            </div>
          )}

          {svc.data?.last_error && <p className="text-xs text-[var(--text-muted)]">Last service error: {svc.data.last_error}</p>}

          <p className="text-xs text-[var(--text-muted)]">
            While the service is stopped, <Link className="underline" to="/security">Security</Link> and{" "}
            <Link className="underline" to="/logs">Diagnostics</Link> stay available so you can inspect the last state before restarting.
          </p>

          <Dialog open={confirmAction !== ""} onOpenChange={(open) => { if (!open) setConfirmAction(""); }}>
            <DialogContent>
              <DialogTitle>
                {confirmAction === "install" ? "Install the service?" : confirmAction === "repair" ? "Repair the service?" : "Uninstall the service?"}
              </DialogTitle>
              <DialogDescription>
                {confirmAction === "install" &&
                  "This registers the Skein daemon as a system service (root/SYSTEM) through the minimal helper and may ask for administrator approval once. It does not give the app a shell or ongoing administrator access."}
                {confirmAction === "repair" &&
                  "Repair stops the service, re-registers the daemon binary, then starts it again. It may ask for administrator approval and briefly takes this device off the mesh."}
                {confirmAction === "uninstall" &&
                  "This stops and removes the system service. Your mesh configuration and identity stay in place, so Install restores the service later. To delete the network too, cancel and use Switch network / Reset first."}
              </DialogDescription>
              <div className="mt-4 flex justify-end gap-2">
                <Button variant="ghost" disabled={busy} onClick={() => setConfirmAction("")}>
                  Cancel
                </Button>
                <Button
                  variant={confirmAction === "uninstall" ? "danger" : "default"}
                  disabled={busy}
                  onClick={() => {
                    const action = confirmAction;
                    setConfirmAction("");
                    if (action) service.mutate(action);
                  }}
                >
                  {confirmAction === "install" ? "Install" : confirmAction === "repair" ? "Repair" : "Uninstall"}
                </Button>
              </div>
            </DialogContent>
          </Dialog>
        </CardContent>
      </Card>

      {c && (
        <Card>
          <CardHeader>
            <CardTitle>Effective configuration</CardTitle>
            <Badge tone="neutral">read-only</Badge>
          </CardHeader>
          <CardContent>
            <dl className="grid gap-3 sm:grid-cols-2">
              {[
                ["overlay IPv4", c.overlay_ipv4 || "—"],
                ["overlay IPv6", c.overlay_ipv6 || "—"],
                ["MTU", c.effective_mtu ? `${c.effective_mtu} live · ${c.mtu} floor` : `${c.mtu} floor`],
                ["interface", c.interface || "—"],
                ["STUN", c.stun ? c.stun_server || "on" : "off"],
                ["config path", c.config_path || "—"],
                ["API socket", c.api_socket || c.api_pipe || "—"],
              ].map(([label, value]) => (
                <div key={label} className="min-w-0">
                  <dt className="mono-label">{label}</dt>
                  <dd className="mt-1 truncate font-mono text-xs" title={value}>
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
            <p className="mt-3 text-xs text-[var(--text-muted)]">
              Reported by the running daemon. Secret fields are redacted before they reach the app.
            </p>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Appearance</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-6">
          <Choice<Theme>
            label="theme"
            value={theme}
            onChange={setTheme}
            options={[
              ["dark", "Dark"],
              ["light", "Light"],
              ["hc", "High contrast"],
            ]}
          />
          <Choice<Motion>
            label="motion"
            value={motion}
            onChange={setMotion}
            options={[
              ["system", "Follow system"],
              ["reduced", "Reduced"],
              ["full", "Full"],
            ]}
          />
        </CardContent>
      </Card>
    </div>
  );
}
