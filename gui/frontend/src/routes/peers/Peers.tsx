import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Search, Wifi } from "lucide-react";
import { PeerService, errText, errKind } from "@/lib/api";
import { hasLeaveBinding, callLeaveNetwork } from "@/lib/bindings";
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
  // Step 12 (ADR-002): no peer removal — no peer removes another peer.
  // The only departure action is this node leaving itself. M7: the Go
  // method exists (gui/services/peer.go LeaveNetwork) but the
  // checked-in binding does not export it yet, so the button is
  // disabled with the reason until a Wails build regenerates
  // bindings (see lib/bindings.ts); after regeneration this exact
  // mutation runs. M4 leave gossip already landed: propagated is
  // true only when the departure announcement reached at least one
  // peer; false means no peer was reached, and offline peers learn
  // it from the retained tombstone on reconnect — it is not a ban,
  // and the leaver keeps its credential until a network-wide secret
  // rotation, so rejoin needs a fresh invite.
  const leaveAvailable = hasLeaveBinding();
  const leave = useMutation({
    mutationFn: () => callLeaveNetwork(),
    onSuccess: (r) => { toast.success(r.propagated ? `Left the network (${r.peers_left} peers left, ${r.routes_withdrawn} routes withdrawn)` : `Left locally — no peer was reached to announce it to; offline peers learn it from the retained tombstone on reconnect. Rejoin needs a fresh invite.`); qc.invalidateQueries({ queryKey: ["peers"] }); },
    onError: (e) => toast.error(`${errKind(e) ? errKind(e) + ": " : ""}${errText(e)}`),
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
          <CardHeader><CardTitle>{filtered.length} peers</CardTitle>
            <Dialog>
              <DialogTrigger asChild><Button size="sm" variant="secondary" disabled={!leaveAvailable || leave.isPending} title={leaveAvailable ? "Leave this network yourself" : "Leave needs regenerated Wails bindings (see notice below)"}>Leave network</Button></DialogTrigger>
              <DialogContent>
                <DialogTitle>Leave this network?</DialogTitle>
                <DialogDescription>
                  This device leaves the mesh itself. No other peer is removed by you, and leaving is not a ban: this device keeps its network credential until the whole network rotates its secret, so rejoining needs a fresh invite from a member.
                  Leaving first announces the departure to reachable peers (their tables and routes update); peers that are offline learn it from the retained tombstone when they reconnect. The dataplane stops and sessions close.
                </DialogDescription>
                <div className="mt-4 flex justify-end gap-2"><Button variant="secondary" onClick={() => leave.mutate()} disabled={leave.isPending}>Leave network</Button></div>
              </DialogContent>
            </Dialog>
          </CardHeader>
          {!leaveAvailable && <div className="px-4 pb-3 text-xs text-[var(--text-muted)]">Leave network is not callable in this build: the LeaveNetwork Wails binding has not been regenerated since Step 8. Run a Wails build on a GUI machine to regenerate bindings; the button then activates with no further code change.</div>}
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
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
