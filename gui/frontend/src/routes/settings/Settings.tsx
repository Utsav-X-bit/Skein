import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Save, Server } from "lucide-react";
import { ConfigService, ServiceService, errText, type ConfigView } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { ErrorState } from "@/components/ui/error-state";
import { toast } from "sonner";

export function Settings() {
  const cfg = useQuery({ queryKey: ["config"], queryFn: () => ConfigService.Get(), refetchInterval: 5_000 });
  const svc = useQuery({ queryKey: ["service"], queryFn: () => ServiceService.Status(), refetchInterval: 3_000 });
  const [name, setName] = useState("");
  const [port, setPort] = useState("");
  const [mtu, setMtu] = useState("");
  const [mdns, setMdns] = useState(true);
  useEffect(() => { if (cfg.data) { setName(cfg.data.name ?? ""); setPort(String(cfg.data.listen_port)); setMtu(String(cfg.data.mtu)); setMdns(cfg.data.mdns); } }, [cfg.data]);
  const patch = useMutation({
    mutationFn: () => ConfigService.Patch({ name, listen_port: Number(port), mtu: Number(mtu), mdns } as never),
    onSuccess: (r) => { toast.success(`Applied ${(r.applied ?? []).join(", ")}${r.restart_required ? " · restart required" : ""}`); cfg.refetch(); },
    onError: (e) => toast.error(errText(e)),
  });
  const serviceAction = useMutation({ mutationFn: (a: "start" | "stop" | "restart") => a === "start" ? ServiceService.Start() : a === "stop" ? ServiceService.Stop() : ServiceService.Restart(), onError: (e) => toast.error(errText(e)), onSuccess: () => svc.refetch() });
  if (cfg.isError) return <ErrorState title="Cannot load settings" message={errText(cfg.error)} onRetry={() => cfg.refetch()} />;
  if (!cfg.data) return <div className="h-64 animate-pulse rounded-lg bg-[var(--surface-raised)]" />;
  const c: ConfigView = cfg.data;
  return (
    <div className="space-y-4">
      <div><h1 className="text-2xl font-medium tracking-tight">Settings</h1><p className="text-sm text-[var(--text-muted)]">Only fields the daemon can apply safely are editable. Secrets stay in the root-owned config.</p></div>
      <Card>
        <CardHeader><CardTitle>Node</CardTitle><Badge tone="neutral">protocol v{c.protocol}</Badge></CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <div><Label>name</Label><Input value={name} onChange={(e) => setName(e.target.value)} maxLength={32} /></div>
          <div><Label>listen port</Label><Input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" /></div>
          <div><Label>mtu</Label><Input value={mtu} onChange={(e) => setMtu(e.target.value)} inputMode="numeric" /></div>
          <div className="flex items-end gap-3"><Switch checked={mdns} onCheckedChange={setMdns} /><span className="text-sm">Local discovery (mDNS)</span></div>
          <div className="sm:col-span-2 flex justify-end"><Button onClick={() => patch.mutate()} disabled={patch.isPending}><Save size={15} /> Apply</Button></div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>Service</CardTitle><Server size={16} /></CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-3 text-sm"><Badge tone={svc.data?.state === "running" ? "success" : "neutral"}>{svc.data?.state ?? "unknown"}</Badge><span className="text-[var(--text-muted)]">{svc.data?.manager}</span></div>
          <div className="flex flex-wrap gap-2"><Button variant="secondary" onClick={() => serviceAction.mutate("start")}>Start</Button><Button variant="secondary" onClick={() => serviceAction.mutate("stop")}>Stop</Button><Button variant="secondary" onClick={() => serviceAction.mutate("restart")}>Restart</Button></div>
          <p className="text-xs text-[var(--text-faint)]">Service mutations use the root-owned helper and may prompt for elevation.</p>
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
