import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Save, Server } from "lucide-react";
import { ConfigService, ServiceService, errText, errKind, type ConfigView } from "@/lib/api";
import { hasInstallWithDaemonBinding, hasUninstallWithPurgeBinding } from "@/lib/bindings";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { ErrorState } from "@/components/ui/error-state";
import { toast } from "sonner";

export function Settings() {
  // Step 8: the 5s poll used to clobber the form on every refetch,
  // discarding edits mid-typing. Track dirtiness: while the user has
  // edited anything, the poll pauses and server data no longer
  // overwrites the form; a successful apply clears dirtiness.
  const [dirty, setDirty] = useState(false);
  const cfg = useQuery({ queryKey: ["config"], queryFn: () => ConfigService.Get(), refetchInterval: dirty ? false : 5_000 });
  const svc = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), refetchInterval: 3_000 });
  const [name, setName] = useState("");
  const [port, setPort] = useState("");
  const [mtu, setMtu] = useState("");
  const [mdns, setMdns] = useState(true);
  useEffect(() => { if (cfg.data && !dirty) { setName(cfg.data.name ?? ""); setPort(String(cfg.data.listen_port)); setMtu(String(cfg.data.mtu)); setMdns(cfg.data.mdns); } }, [cfg.data, dirty]);
  const patch = useMutation({
    mutationFn: () => ConfigService.Patch({ name, listen_port: Number(port), mtu: Number(mtu), mdns } as never),
    onSuccess: (r) => { toast.success(`Applied ${(r.applied ?? []).join(", ")}${r.restart_required ? " · restart required" : ""}`); setDirty(false); cfg.refetch(); },
    onError: (e) => toast.error(errText(e)),
  });
  const serviceAction = useMutation({ mutationFn: (a: "start" | "stop" | "restart") => a === "start" ? ServiceService.Start() : a === "stop" ? ServiceService.Stop() : ServiceService.Restart(), onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)}`), onSuccess: () => svc.refetch() });
  // M7 install / uninstall / repair (retained helper verbs, bound
  // bindings only). InstallWithDaemon and UninstallWithPurge are NOT
  // in the checked-in bindings yet, so Install uses the helper's
  // resolved daemon path and Uninstall cannot purge config/identity
  // from here — both limits are stated in the UI, not hidden.
  // Repair is stop + install (which re-registers the daemon via the
  // RegisterDaemon path) + start, in that order, stopping at the
  // first failed step with that step named in the error.
  const [confirmAction, setConfirmAction] = useState<"" | "install" | "uninstall" | "repair">("");
  const [repairStep, setRepairStep] = useState("");
  const install = useMutation({ mutationFn: () => ServiceService.Install(), onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}Install failed: ${errText(e)}`), onSuccess: () => { toast.success("Service installed"); svc.refetch(); } });
  const uninstall = useMutation({ mutationFn: () => ServiceService.Uninstall(), onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}Uninstall failed: ${errText(e)}`), onSuccess: () => { toast.success("Service uninstalled (config and identity kept — purge needs a regenerated binding)"); svc.refetch(); } });
  const repair = useMutation({
    mutationFn: async () => {
      for (const [step, fn] of [["stopping", ServiceService.Stop], ["reinstalling", ServiceService.Install], ["starting", ServiceService.Start]] as const) {
        setRepairStep(step);
        try { await fn(); } catch (e) { throw new Error(`repair failed while ${step}: ${errText(e)}`); }
      }
    },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)} — service state is whatever that step left; check the status above before retrying.`),
    onSuccess: () => { toast.success("Service repaired: stopped, reinstalled, started"); svc.refetch(); },
    onSettled: () => setRepairStep(""),
  });
  const busy = serviceAction.isPending || install.isPending || uninstall.isPending || repair.isPending;
  if (cfg.isError) return <ErrorState title="Cannot load settings" message={errText(cfg.error)} onRetry={() => cfg.refetch()} />;
  if (!cfg.data) return <div className="h-64 animate-pulse rounded-lg bg-[var(--surface-raised)]" />;
  const c: ConfigView = cfg.data;
  return (
    <div className="space-y-4">
      <div><h1 className="text-2xl font-medium tracking-tight">Settings</h1><p className="text-sm text-[var(--text-muted)]">Only fields the daemon can apply safely are editable. Secrets stay in the root-owned config.</p></div>
      <Card>
        <CardHeader><CardTitle>Node</CardTitle><Badge tone="neutral">protocol v{c.protocol}</Badge></CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <div><Label>name</Label><Input value={name} onChange={(e) => { setName(e.target.value); setDirty(true); }} maxLength={32} /></div>
          <div><Label>listen port</Label><Input value={port} onChange={(e) => { setPort(e.target.value); setDirty(true); }} inputMode="numeric" /></div>
          <div><Label>mtu</Label><Input value={mtu} onChange={(e) => { setMtu(e.target.value); setDirty(true); }} inputMode="numeric" /></div>
          <div className="flex items-end gap-3"><Switch checked={mdns} onCheckedChange={(v) => { setMdns(v); setDirty(true); }} /><span className="text-sm">Local discovery (mDNS)</span></div>
          <div className="sm:col-span-2 flex justify-end"><Button onClick={() => patch.mutate()} disabled={patch.isPending}><Save size={15} /> Apply</Button></div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>Service</CardTitle><Server size={16} /></CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-3 text-sm"><Badge tone={svc.data?.state === "running" ? "success" : "neutral"}>{svc.data?.state ?? "unknown"}</Badge><span className="text-[var(--text-muted)]">{svc.data?.manager}</span></div>
          <div className="rounded-md border border-[var(--border)] bg-[var(--surface-raised)] p-3 text-xs text-[var(--text-muted)]">
            <span className="font-medium text-[var(--text)]">Before any elevation, what each action does:</span> these actions run only through the minimal helper&apos;s fixed verbs — the app never gets a shell and never keeps root access afterwards.
            Install registers the daemon as the system service (root/SYSTEM) and is the only step that creates it. Start / Stop / Restart only change that service&apos;s lifecycle. Uninstall stops and removes the service.
            Repair stops the service, reinstalls it (re-registering the daemon binary), then starts it again, stopping at the first failed step. Every action may prompt once for administrator approval (pkexec on Linux, UAC on Windows); denying it cancels just that action.
            {!hasInstallWithDaemonBinding() && " Install currently uses the helper-resolved daemon path (the explicit-daemon binding is not generated yet)."}
            {!hasUninstallWithPurgeBinding() && " Uninstall from here keeps config and identity (the purge binding is not generated yet); purging needs a regenerated binding or the terminal uninstall."}
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" disabled={busy} onClick={() => serviceAction.mutate("start")}>Start</Button>
            <Button variant="secondary" disabled={busy} onClick={() => serviceAction.mutate("stop")}>Stop</Button>
            <Button variant="secondary" disabled={busy} onClick={() => serviceAction.mutate("restart")}>Restart</Button>
            <Button variant="secondary" disabled={busy} onClick={() => setConfirmAction("install")}>Install</Button>
            <Button variant="secondary" disabled={busy} onClick={() => setConfirmAction("repair")}>Repair</Button>
            <Button variant="secondary" disabled={busy} onClick={() => setConfirmAction("uninstall")}>Uninstall</Button>
          </div>
          {repairStep && <p className="text-xs text-[var(--text-muted)]">Repair: {repairStep}…</p>}
          {svc.data?.last_error && <p className="text-xs text-[var(--text-muted)]">Last service error: {svc.data.last_error}</p>}
          <Dialog open={confirmAction !== ""} onOpenChange={(o) => { if (!o) setConfirmAction(""); }}>
            <DialogContent>
              <DialogTitle>{confirmAction === "install" ? "Install the service?" : confirmAction === "repair" ? "Repair the service?" : "Uninstall the service?"}</DialogTitle>
              <DialogDescription>
                {confirmAction === "install" && "This registers the Skein daemon as a system service (root/SYSTEM) via the minimal helper and may prompt for administrator approval once. It does not give the app a shell or ongoing root access."}
                {confirmAction === "repair" && "Repair stops the service, reinstalls it (re-registering the daemon binary), then starts it. It may prompt for administrator approval and briefly takes the mesh offline on this device."}
                {confirmAction === "uninstall" && "This stops and removes the system service. Your mesh config and identity are kept (purge is not available from this screen yet); the daemon and this app stay installed, and you can install the service again afterwards."}
              </DialogDescription>
              <div className="mt-4 flex justify-end gap-2"><Button variant="secondary" disabled={busy} onClick={() => { const a = confirmAction; setConfirmAction(""); if (a === "install") install.mutate(); else if (a === "repair") repair.mutate(); else if (a === "uninstall") uninstall.mutate(); }}>{confirmAction === "install" ? "Install" : confirmAction === "repair" ? "Repair" : "Uninstall"}</Button></div>
            </DialogContent>
          </Dialog>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>Configuration</CardTitle></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          {[["overlay IPv4", c.overlay_ipv4], ["overlay IPv6", c.overlay_ipv6], ["interface", c.interface], ["API socket", c.api_socket], ["config path", c.config_path], ["STUN", c.stun ? c.stun_server : "off"]].map(([k, v]) => <div key={k}><div className="mono-label">{k}</div><div className="mt-1 truncate font-mono text-xs">{v || "—"}</div></div>)}
        </CardContent>
      </Card>
    </div>
  );
}
