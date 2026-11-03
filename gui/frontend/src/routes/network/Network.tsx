import { useQuery } from "@tanstack/react-query";
import { NetworkService, PeerService, errText } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorState } from "@/components/ui/error-state";
import { EmptyState } from "@/components/ui/empty-state";
import { Topology } from "@/features/topology/Topology";
import { shortID } from "@/lib/utils";

export function Network() {
  const status = useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), refetchInterval: 2_000 });
  const peers = useQuery({ queryKey: ["peers"], queryFn: () => PeerService.List(), refetchInterval: 2_000 });
  const routes = useQuery({ queryKey: ["routes"], queryFn: () => NetworkService.Routes(), refetchInterval: 2_000 });
  if (status.isError) return <ErrorState title="Cannot load topology" message={errText(status.error)} onRetry={() => status.refetch()} />;
  if (!status.data) return <div className="h-96 animate-pulse rounded-lg bg-[var(--surface-raised)]" />;
  const ps = peers.data ?? [];
  const rs = routes.data ?? [];
  return (
    <div className="space-y-4">
      <div><h1 className="text-2xl font-medium tracking-tight">Network</h1><p className="text-sm text-[var(--text-muted)]">Live topology and the routes this node actually uses.</p></div>
      <Topology status={status.data} peers={ps} />
      <Card>
        <CardHeader><CardTitle>Routes</CardTitle><span className="mono-label">distance vector · max 4 hops</span></CardHeader>
        <CardContent className="p-0">
          {rs.length === 0 ? <EmptyState title="No routes yet" description="Routes appear when peers authenticate." className="m-4" /> : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="border-b border-[var(--border)] text-[var(--text-faint)]"><tr>{["Destination", "Next hop", "Cost", "Hops", "Path"].map((h) => <th key={h} className="mono-label px-4 py-2 font-normal">{h}</th>)}</tr></thead>
                <tbody className="divide-y divide-[var(--border)]">
                  {rs.map((r) => (
                    <tr key={r.destination} className="hover:bg-[var(--surface-raised)]">
                      <td className="px-4 py-2 font-mono text-xs">{shortID(r.destination)}</td>
                      <td className="px-4 py-2 font-mono text-xs">{shortID(r.next_hop)}</td>
                      <td className="px-4 py-2 tabular">{r.cost_ms.toFixed(1)} ms</td>
                      <td className="px-4 py-2">{r.hops}</td>
                      <td className="px-4 py-2 font-mono text-xs text-[var(--text-muted)]">{(r.path ?? []).map(shortID).join(" → ")}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
      <Card><CardHeader><CardTitle>Exit nodes & subnet routes</CardTitle></CardHeader><CardContent><p className="text-sm text-[var(--text-muted)]">Not supported by this Skein engine. The GUI does not show controls that the daemon cannot honor.</p></CardContent></Card>
    </div>
  );
}
