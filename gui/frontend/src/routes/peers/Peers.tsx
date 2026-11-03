import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Search, Trash2, Wifi } from "lucide-react";
import { PeerService, errText } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { StatusDot } from "@/components/ui/status-dot";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { formatBytes, shortID, timeAgo } from "@/lib/utils";
import { toast } from "sonner";

export function Peers() {
  const qc = useQueryClient();
  const [q, setQ] = useState("");
  const [invite, setInvite] = useState("");
  const peers = useQuery({ queryKey: ["peers"], queryFn: () => PeerService.List(), refetchInterval: 2_000 });
  const filtered = useMemo(() => (peers.data ?? []).filter((p) => `${p.name ?? ""} ${p.id} ${p.overlay_ipv4 ?? ""}`.toLowerCase().includes(q.toLowerCase())), [peers.data, q]);
  const add = useMutation({
    mutationFn: () => PeerService.Add({ invite } as never),
    onSuccess: () => { setInvite(""); toast.success("Device added"); qc.invalidateQueries({ queryKey: ["peers"] }); },
    onError: (e) => toast.error(errText(e)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => PeerService.Remove(id),
    onSuccess: () => { toast.success("Peer removed from this device"); qc.invalidateQueries({ queryKey: ["peers"] }); },
    onError: (e) => toast.error(errText(e)),
  });
  const probe = useMutation({
    mutationFn: (id: string) => PeerService.Probe(id),
    onSuccess: (r) => toast.info(`Probe ${r.state}${r.rtt_ms ? ` · ${r.rtt_ms.toFixed(1)} ms` : ""}`),
    onError: (e) => toast.error(errText(e)),
  });

  if (peers.isError) return <ErrorState title="Cannot load peers" message={errText(peers.error)} onRetry={() => peers.refetch()} />;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div><h1 className="text-2xl font-medium tracking-tight">Peers</h1><p className="text-sm text-[var(--text-muted)]">Devices in this mesh and their live paths.</p></div>
        <Dialog>
          <DialogTrigger asChild><Button><Plus size={16} /> Add device</Button></DialogTrigger>
          <DialogContent>
            <DialogTitle>Add a device</DialogTitle>
            <DialogDescription>Paste a <code>skein:1:</code> peer invite. This introduces the device; it does not change network membership.</DialogDescription>
            <Input className="mt-4" placeholder="skein:1:…" value={invite} onChange={(e) => setInvite(e.target.value)} />
            <div className="mt-4 flex justify-end gap-2"><Button variant="secondary" onClick={() => setInvite("")}>Clear</Button><Button disabled={!invite || add.isPending} onClick={() => add.mutate()}>Add</Button></div>
          </DialogContent>
        </Dialog>
      </div>
      <div className="flex items-center gap-2 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3">
        <Search size={15} className="text-[var(--text-faint)]" />
        <Input className="border-0 bg-transparent focus-visible:ring-0" placeholder="Search peers…" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      {filtered.length === 0 ? <EmptyState title="No peers" description="Invite a device from Invitations, or add a peer invite here." /> : (
        <Card>
          <CardHeader><CardTitle>{filtered.length} peers</CardTitle><span className="mono-label">remove is local-only</span></CardHeader>
          <CardContent className="divide-y divide-[var(--border)] p-0">
            {filtered.map((p) => (
              <div key={p.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
                <div className="flex min-w-0 items-center gap-3">
                  <StatusDot tone={p.state === "up" ? "up" : p.state === "degraded" ? "degraded" : "down"} pulse={p.state === "up"} />
                  <div className="min-w-0"><div className="truncate text-sm font-medium">{p.name || shortID(p.id)}</div><div className="mono-label truncate">{p.overlay_ipv4 || p.overlay_ipv6 || p.id}</div></div>
                </div>
                <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--text-muted)]">
                  <Badge tone={p.direct ? "accent" : "relay"}>{p.direct ? "direct" : "relay"}</Badge>
                  <span>{p.rtt_ms ? `${p.rtt_ms.toFixed(1)} ms` : "—"}</span>
                  <span>↓ {formatBytes(p.rx_bytes)} ↑ {formatBytes(p.tx_bytes)}</span>
                  <span>{timeAgo(p.last_seen_ms ? Date.now() - p.last_seen_ms : undefined)}</span>
                  <Button size="sm" variant="ghost" onClick={() => probe.mutate(p.id)} aria-label={`Probe ${p.name || p.id}`}><Wifi size={15} /></Button>
                  <Button size="sm" variant="ghost" onClick={() => { if (confirm(`Remove ${p.name || p.id} from this device? This does not revoke access.`)) remove.mutate(p.id); }} aria-label={`Remove ${p.name || p.id}`}><Trash2 size={15} /></Button>
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
