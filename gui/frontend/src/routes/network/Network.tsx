import { useMemo, useState } from "react";
import { Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { displayPeerNameById, shortID } from "@/lib/utils";
import { useLiveSampler, useMesh } from "@/features/mesh/useMesh";
import { routeSummary } from "@/features/mesh/model";
import { rowKeyNav, usePeerParam } from "@/features/mesh/hooks";
import { PeerInspector } from "@/features/mesh/PeerInspector";

type SortKey = "destination" | "nextHop" | "cost" | "hops";

export function Network() {
  useLiveSampler(true);
  const mesh = useMesh();
  const [peerId, setPeerId] = usePeerParam();
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "destination", dir: 1 });

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const filtered = mesh.model.paths.filter((p) =>
      !needle || [p.destination, p.nextHop, ...p.path].join(" ").toLowerCase().includes(needle),
    );
    return [...filtered].sort((a, b) => {
      const order =
        sort.key === "destination"
          ? a.destination.localeCompare(b.destination)
          : sort.key === "nextHop"
            ? a.nextHop.localeCompare(b.nextHop)
            : sort.key === "cost"
              ? a.costMs - b.costMs
              : a.hops - b.hops;
      return order * sort.dir;
    });
  }, [mesh.model.paths, q, sort]);

  if (mesh.loading && !mesh.status) return <Skeleton className="h-64" />;
  if (!mesh.status) {
    return <ErrorState title="Cannot load routes" message={mesh.error ?? "No status snapshot has arrived."} onRetry={mesh.refetch} />;
  }

  const self = mesh.status.id;
  const status = mesh.status;
  const nameOf = (id: string) => displayPeerNameById(id, mesh.peers);
  const toggle = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, dir: s.dir === 1 ? -1 : 1 } : { key, dir: 1 }));
  const arrow = (key: SortKey) => (sort.key === key ? (sort.dir === 1 ? "↑" : "↓") : "");

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-medium tracking-tight">Routes</h1>
        <p className="text-sm text-[var(--text-muted)]">The resolved paths this node actually uses. Paths exclude this device.</p>
      </div>

      <div className="flex items-center gap-2 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3">
        <Search size={15} className="text-[var(--text-faint)]" />
        <Input className="border-0 bg-transparent focus-visible:ring-0" placeholder="Search destination, next hop or path…" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search routes" />
      </div>

      <div className="flex flex-col gap-4 min-[1100px]:flex-row">
        <Card className="min-w-0 flex-1">
          <CardHeader>
            <CardTitle>{rows.length} routes</CardTitle>
            <span className="mono-label">distance vector · max 4 hops</span>
          </CardHeader>
          <CardContent className="p-0">
            {rows.length === 0 ? (
              <EmptyState
                title={mesh.model.paths.length === 0 ? "No routes yet" : "No matching routes"}
                description={mesh.model.paths.length === 0 ? "Routes appear when peers authenticate and advertise a path." : "Clear the search to see every resolved path."}
                className="m-4"
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[560px] text-left text-sm" aria-label="Routes">
                  <thead className="border-b border-[var(--border)] text-[var(--text-faint)]">
                    <tr>
                      {([["destination", "Destination"], ["nextHop", "Next hop"], ["cost", "Cost"], ["hops", "Hops"]] as const).map(([key, label]) => (
                        <th key={key} className="mono-label px-3 py-2 font-normal" aria-sort={sort.key === key ? (sort.dir === 1 ? "ascending" : "descending") : "none"}>
                          <Button variant="ghost" size="sm" className="-ml-2 h-6 px-2 text-[10px]" onClick={() => toggle(key)}>
                            {label} {arrow(key)}
                          </Button>
                        </th>
                      ))}
                      <th className="mono-label px-3 py-2 font-normal">Full path</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[var(--border)]">
                    {rows.map((p) => {
                      const chain = p.path.length > 0 ? [self, ...p.path] : [self, p.destination];
                      const selected = peerId === p.destination;
                      return (
                        <tr
                          key={p.destination}
                          data-row
                          tabIndex={0}
                          aria-selected={selected}
                          onClick={() => setPeerId(p.destination)}
                          onKeyDown={(e) => rowKeyNav(e, () => setPeerId(p.destination), () => setPeerId(null))}
                          className={`cursor-pointer hover:bg-[var(--surface-raised)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)] ${selected ? "bg-[var(--accent-soft)]" : ""}`}
                        >
                          <td className="px-3 py-2">
                            <div className="truncate">{nameOf(p.destination)}</div>
                            <div className="font-mono text-[10px] text-[var(--text-faint)]">{nameOf(p.destination) !== shortID(p.destination) ? shortID(p.destination) : "—"}</div>
                          </td>
                          <td className="px-3 py-2 text-xs">
                            <div>{nameOf(p.nextHop)}</div>
                            <div className="font-mono text-[10px] text-[var(--text-faint)]">
                              {p.direct ? routeSummary(p, nameOf) : "relay"}
                            </div>
                          </td>
                          <td className="tabular px-3 py-2">{p.costMs.toFixed(1)} ms</td>
                          <td className="px-3 py-2">{p.hops}</td>
                          <td className="px-3 py-2">
                            <div className="flex flex-wrap items-center gap-1 font-mono text-[10px]">
                              {chain.map((hop, i) => (
                                <span key={`${hop}-${i}`} className={selected ? "text-[var(--accent)]" : "text-[var(--text-muted)]"}>
                                  {i === 0 ? "you" : nameOf(hop)}{i < chain.length - 1 ? " →" : ""}
                                </span>
                              ))}
                            </div>
                          </td>
                        </tr>
                      );
                    })}
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

      <Card>
        <CardHeader>
          <CardTitle>{mesh.links.length} advertised routes</CardTitle>
          <span className="mono-label">one row per direction · via names the first hop</span>
        </CardHeader>
        <CardContent className="p-0">
          {mesh.links.length === 0 ? (
            <EmptyState
              title="No peer links advertised"
              description="Neighbours advertise how they reach other members. Links appear after the next state exchange."
              className="m-4"
            />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[420px] text-left text-sm" aria-label="Peer links">
                <thead className="border-b border-[var(--border)] text-[var(--text-faint)]">
                  <tr>
                    {["From", "To", "Via", "Cost"].map((h) => (
                      <th key={h} className="mono-label px-3 py-2 font-normal">{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {mesh.links.map((l) => {
                    const via =
                      l.hops === 1 ? "direct" : l.path?.[1] === self ? "you" : nameOf(l.path?.[1] ?? l.destination);
                    return (
                      <tr
                        key={`${l.from}>${l.destination}`}
                        data-row
                        tabIndex={0}
                        onClick={() => setPeerId(l.destination)}
                        onKeyDown={(e) => rowKeyNav(e, () => setPeerId(l.destination), () => setPeerId(null))}
                        className="cursor-pointer hover:bg-[var(--surface-raised)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)]"
                      >
                        <td className="px-3 py-2">{nameOf(l.from)}</td>
                        <td className="px-3 py-2">{nameOf(l.destination)}</td>
                        <td className="px-3 py-2 text-xs text-[var(--text-muted)]">{via}</td>
                        <td className="tabular px-3 py-2">{l.cost_ms.toFixed(1)} ms</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
