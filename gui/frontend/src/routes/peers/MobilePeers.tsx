import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Plus, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { MobileSheet } from "@/components/layout/MobileSheet";
import { useLiveSampler, useMesh } from "@/features/mesh/useMesh";
import { PeerHealth } from "@/features/mesh/PeerHealth";
import { PeerInspectorBody, useLeaveNetworkMutation } from "@/features/mesh/PeerInspector";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { formatBytesPerSec, presenceOf, routeSummary, type Presence, type Rate } from "@/features/mesh/model";
import { displayNodeName, displayPeerName, displayPeerNameById } from "@/lib/utils";

const presenceRank: Record<Presence, number> = { active: 0, idle: 1, routed: 2, offline: 3, unknown: 4 };

/**
 * MobilePeers is the Peers tab (design brief §3.2): presence-sorted
 * cards, "This device" pinned, search, tap → detail sheet, and a
 * "+ Invite device" action. Offline cards are dimmed; there is no
 * remove-peer action anywhere (F-01, deferred).
 */
export function MobilePeers() {
  useLiveSampler(true);
  const mesh = useMesh();
  const [q, setQ] = useState("");
  const [peerId, setPeerId] = useState<string | null>(null);
  const [confirmLeave, setConfirmLeave] = useState(false);
  const leave = useLeaveNetworkMutation();

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const now = Date.now();
    const nameOf = (id: string) => displayPeerNameById(id, mesh.peers);
    return mesh.peers
      .filter((p) => !needle || `${p.name ?? ""} ${p.id} ${p.overlay_ipv4 ?? ""} ${p.path ?? ""}`.toLowerCase().includes(needle))
      .map((peer) => {
        const route = mesh.model.paths.find((p) => p.destination === peer.id);
        return {
          peer,
          presence: presenceOf(peer, mesh.lastTrafficAt[peer.id], now, route !== undefined && !route.direct),
          rate: mesh.peerRates[peer.id] as Rate | undefined,
          routeLabel: routeSummary(route, nameOf),
        };
      })
      .sort((a, b) => presenceRank[a.presence] - presenceRank[b.presence] || a.peer.rtt_ms - b.peer.rtt_ms);
  }, [mesh.peers, mesh.lastTrafficAt, mesh.peerRates, mesh.model.paths, q]);

  if (mesh.loading && !mesh.status) return <Skeleton className="h-64" />;
  if (!mesh.status) {
    return <ErrorState title="Cannot load peers" message={mesh.error ?? "No status snapshot has arrived."} onRetry={mesh.refetch} />;
  }
  const status = mesh.status;
  const self = status.id;
  const up = mesh.peers.filter((p) => p.state === "up").length;

  const selectedName = peerId === self ? displayNodeName(status.name) : peerId ? displayPeerNameById(peerId, mesh.peers) : "";

  return (
    <div className="space-y-4 pb-24">
      <div>
        <h1 className="text-2xl font-medium tracking-tight">Peers</h1>
        <p className="text-sm text-[var(--text-muted)]">
          {up} of {mesh.peers.length} up · {displayNodeName(status.name)} · {status.overlay_ipv4 ?? ""}
        </p>
      </div>

      <div className="relative">
        <Search size={16} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-faint)]" />
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search peers"
          aria-label="Search peers"
          className="h-12 pl-9"
        />
      </div>

      {/* This device, pinned */}
      <button
        type="button"
        onClick={() => setPeerId(self)}
        className="w-full rounded-lg border border-[var(--border-strong)] bg-[var(--surface-raised)] p-3 text-left"
      >
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium">{displayNodeName(status.name)}</span>
          <span className="rounded-full bg-[var(--accent-soft)] px-2 py-0.5 text-[11px] text-[var(--accent)]">this device</span>
        </div>
        <div className="mt-1 font-mono text-xs text-[var(--text-muted)]">{status.overlay_ipv4 ?? ""}</div>
        <div className="mt-1 text-xs text-[var(--text-muted)]">{status.connection?.state ?? "unknown"} · {mesh.peers.length} peer{mesh.peers.length === 1 ? "" : "s"}</div>
      </button>

      {rows.length === 0 && mesh.peers.length === 0 && (
        <EmptyState
          title="No peers yet"
          description="This mesh has only this device so far. Invite another device to join."
          action={
            <Link to="/invites">
              <Button>Invite a device</Button>
            </Link>
          }
        />
      )}
      {rows.length === 0 && mesh.peers.length > 0 && (
        <EmptyState title="No matches" description={`No peer matches “${q}”.`} />
      )}

      <ul className="space-y-2">
        {rows.map(({ peer, presence, rate, routeLabel }) => {
          const offline = presence === "offline" || presence === "unknown";
          return (
            <li key={peer.id}>
              <button
                type="button"
                onClick={() => setPeerId(peer.id)}
                className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface)] p-3 text-left"
                style={offline ? { opacity: 0.6 } : undefined}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="min-w-0 truncate font-medium">{displayPeerName(peer)}</span>
                  <PeerHealth peer={peer} presence={presence} />
                </div>
                <div className="mt-1 truncate font-mono text-xs text-[var(--text-muted)]">
                  {peer.overlay_ipv4 || peer.overlay_ipv6 || "no overlay address yet"}
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-[var(--text-muted)]">
                  <span className="tabular">{peer.rtt_ms > 0 ? `${Math.round(peer.rtt_ms)} ms` : "—"}</span>
                  <span className="tabular">{rate ? `${formatBytesPerSec(rate.rxPerSec)} ↓ · ${formatBytesPerSec(rate.txPerSec)} ↑` : "no traffic"}</span>
                  <span className="truncate">{routeLabel}</span>
                </div>
              </button>
            </li>
          );
        })}
      </ul>

      <Link
        to="/invites"
        className="fixed bottom-[calc(var(--nav-h)+16px)] right-4 z-20 flex h-14 min-w-14 items-center justify-center gap-2 rounded-full bg-[var(--accent)] px-5 text-sm font-medium text-white shadow-lg active:scale-95"
      >
        <Plus size={18} /> Invite device
      </Link>

      <MobileSheet open={peerId !== null} onClose={() => setPeerId(null)} title={selectedName} initialDetent="half">
        <div className="space-y-4">
          <PeerInspectorBody
            peerId={peerId}
            status={status}
            peers={mesh.peers}
            model={mesh.model}
            peerRates={mesh.peerRates}
            lastTrafficAt={mesh.lastTrafficAt}
            onViewRoutes={() => setPeerId(null)}
            onLeaveRequest={() => setConfirmLeave(true)}
          />
        </div>
      </MobileSheet>

      <Dialog open={confirmLeave} onOpenChange={setConfirmLeave}>
        <DialogContent>
          <DialogTitle>Leave this network?</DialogTitle>
          <DialogDescription>
            This device leaves the mesh itself. No other peer is removed by you, and leaving is not a ban: this device keeps its network credential until the whole network rotates its secret, so rejoining needs a fresh invite from a member.
            Leaving first announces the departure to reachable peers (their tables and routes update); peers that are offline learn it from the retained tombstone when they reconnect. The dataplane stops and sessions close.
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setConfirmLeave(false)}>Cancel</Button>
            <Button variant="danger" onClick={() => { leave.mutate(); setConfirmLeave(false); setPeerId(null); }} disabled={leave.isPending}>
              {leave.isPending ? "Leaving…" : "Leave network"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
