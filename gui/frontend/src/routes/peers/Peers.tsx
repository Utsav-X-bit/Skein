import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Plus, Search } from "lucide-react";
import type { Peer } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { shortID } from "@/lib/utils";
import { useLiveSampler, useMesh } from "@/features/mesh/useMesh";
import { rowKeyNav, usePeerParam } from "@/features/mesh/hooks";
import { PeerHealth } from "@/features/mesh/PeerHealth";
import { PeerInspector } from "@/features/mesh/PeerInspector";
import { formatAge, formatBytesPerSec, presenceOf, routeSummary, type Presence, type Rate } from "@/features/mesh/model";

type SortKey = "name" | "presence" | "path" | "transport" | "rtt" | "rate" | "lastSeen";

const presenceRank: Record<Presence, number> = { active: 0, idle: 1, routed: 2, offline: 3, unknown: 4 };

export function Peers() {
  useLiveSampler(true);
  const mesh = useMesh();
  const [peerId, setPeerId] = usePeerParam();
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "name", dir: 1 });

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const now = Date.now();
    const nameOf = (id: string) => mesh.peers.find((p) => p.id === id)?.name || shortID(id);
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
      .sort((a, b) => {
        const order =
          sort.key === "name"
            ? (a.peer.name || a.peer.id).localeCompare(b.peer.name || b.peer.id)
            : sort.key === "presence"
              ? presenceRank[a.presence] - presenceRank[b.presence]
              : sort.key === "path"
                ? a.routeLabel.localeCompare(b.routeLabel)
                : sort.key === "transport"
                  ? (a.peer.transport ?? "").localeCompare(b.peer.transport ?? "")
                  : sort.key === "rtt"
                    ? a.peer.rtt_ms - b.peer.rtt_ms
                    : sort.key === "rate"
                      ? (a.rate ? a.rate.rxPerSec + a.rate.txPerSec : -1) - (b.rate ? b.rate.rxPerSec + b.rate.txPerSec : -1)
                      : a.peer.last_seen_ms - b.peer.last_seen_ms;
        return order * sort.dir;
      });
  }, [mesh.peers, mesh.lastTrafficAt, mesh.peerRates, mesh.model.paths, q, sort]);

  const toggle = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, dir: s.dir === 1 ? -1 : 1 } : { key, dir: 1 }));
  const arrow = (key: SortKey) => (sort.key === key ? (sort.dir === 1 ? "↑" : "↓") : "");

  if (mesh.loading && !mesh.status) return <Skeleton className="h-64" />;
  if (!mesh.status) {
    return <ErrorState title="Cannot load peers" message={mesh.error ?? "No status snapshot has arrived."} onRetry={mesh.refetch} />;
  }
  const status = mesh.status;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-medium tracking-tight">Peers</h1>
          <p className="text-sm text-[var(--text-muted)]">Devices in this mesh and their live paths. This device is listed separately.</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" onClick={() => setPeerId(status.id)}>This device</Button>
          <Button asChild>
            <Link to="/invites"><Plus size={16} /> Invite device</Link>
          </Button>
        </div>
      </div>

      <div className="flex items-center gap-2 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3">
        <Search size={15} className="text-[var(--text-faint)]" />
        <Input className="border-0 bg-transparent focus-visible:ring-0" placeholder="Search peers…" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search peers" />
      </div>

      <div className="flex flex-col gap-4 min-[1100px]:flex-row">
        <Card className="min-w-0 flex-1">
          <CardHeader><CardTitle>{rows.length} peers</CardTitle><span className="mono-label">capacity {1 + mesh.peers.length} of 5</span></CardHeader>
          <CardContent className="p-0">
            {mesh.peers.length === 0 ? (
              <EmptyState
                title="No peers yet"
                description="Invite a device to join this mesh. Membership and connectivity are separate: this daemon can be healthy with no peers."
                action={<Button asChild><Link to="/invites"><Plus size={16} /> Invite a device</Link></Button>}
                className="m-4"
              />
            ) : rows.length === 0 ? (
              <EmptyState title="No matching peers" description="Clear the search to see every peer." className="m-4" />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm" aria-label="Peers">
                  <thead className="border-b border-[var(--border)] text-[var(--text-faint)]">
                    <tr>
                      {([["name", "Name"], ["presence", "Presence"], ["path", "Selected route"], ["transport", "Transport"], ["rtt", "RTT"], ["rate", "Rate"], ["lastSeen", "Last seen"]] as const).map(([key, label]) => (
                        <th key={key} className="mono-label px-3 py-2 font-normal" aria-sort={sort.key === key ? (sort.dir === 1 ? "ascending" : "descending") : "none"}>
                          <Button variant="ghost" size="sm" className="-ml-2 h-6 px-2 text-[10px]" onClick={() => toggle(key)}>
                            {label} {arrow(key)}
                          </Button>
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[var(--border)]">
                    {rows.map(({ peer, presence, rate, routeLabel }) => (
                      <tr
                        key={peer.id}
                        data-row
                        tabIndex={0}
                        aria-selected={peerId === peer.id}
                        onClick={() => setPeerId(peer.id)}
                        onKeyDown={(e) => rowKeyNav(e, () => setPeerId(peer.id), () => setPeerId(null))}
                        className={`cursor-pointer hover:bg-[var(--surface-raised)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)] ${peerId === peer.id ? "bg-[var(--accent-soft)]" : ""}`}
                      >
                        <td className="px-3 py-2">
                          <div className="max-w-[180px] truncate">{peer.name || shortID(peer.id)}</div>
                          <div className="font-mono text-[10px] text-[var(--text-faint)]">{peer.overlay_ipv4 || peer.id}</div>
                        </td>
                        <td className="px-3 py-2"><PeerHealth peer={peer} presence={presence} compact /></td>
                        <td className="px-3 py-2 text-xs">{routeLabel}</td>
                        <td className="px-3 py-2 text-xs">{peer.transport || "unknown"}</td>
                        <td className="tabular px-3 py-2">{peer.rtt_ms > 0 ? `${peer.rtt_ms.toFixed(1)} ms` : "—"}</td>
                        <td className="tabular px-3 py-2 text-xs">
                          {rate ? `↓ ${formatBytesPerSec(rate.rxPerSec)} ↑ ${formatBytesPerSec(rate.txPerSec)}` : "—"}
                        </td>
                        <td className="px-3 py-2 text-xs">{peer.last_seen_ms > 0 ? `${formatAge(peer.last_seen_ms)} ago` : "unknown"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        </Card>

        <PeerInspector
          peerId={peerId}
          status={status}
          peers={mesh.peers}
          model={mesh.model}
          peerRates={mesh.peerRates}
          lastTrafficAt={mesh.lastTrafficAt}
          onClose={() => setPeerId(null)}
          onViewRoutes={(destination) => setPeerId(destination)}
        />
      </div>
    </div>
  );
}
