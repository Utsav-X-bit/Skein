import { useQuery } from "@tanstack/react-query";
import { NetworkService, errText } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorState } from "@/components/ui/error-state";
import { EmptyState } from "@/components/ui/empty-state";
import { formatBytes, shortID } from "@/lib/utils";

export function Activity() {
  const q = useQuery({ queryKey: ["activities"], queryFn: () => NetworkService.Activities(), refetchInterval: 2_000 });
  if (q.isError) return <ErrorState title="Cannot load activity" message={errText(q.error)} onRetry={() => q.refetch()} />;
  const rows = q.data ?? [];
  return (
    <div className="space-y-4">
      <div><h1 className="text-2xl font-medium tracking-tight">Activity</h1><p className="text-sm text-[var(--text-muted)]">Aggregate, payload-free link samples. History is off by default.</p></div>
      <Card>
        <CardHeader><CardTitle>Recent links</CardTitle><span className="mono-label">{rows.length} samples</span></CardHeader>
        <CardContent className="p-0">
          {rows.length === 0 ? <EmptyState title="No samples yet" description="Activity appears when peers exchange traffic." className="m-4" /> : (
            <div className="overflow-x-auto"><table className="w-full text-left text-sm">
              <thead className="border-b border-[var(--border)] text-[var(--text-faint)]"><tr>{["Origin", "Destination", "Transport", "Path", "RX", "TX", "RTT", "Jitter"].map((h) => <th key={h} className="mono-label px-4 py-2 font-normal">{h}</th>)}</tr></thead>
              <tbody className="divide-y divide-[var(--border)]">{rows.map((a, i) => (
                <tr key={`${a.origin}-${a.destination}-${a.seq}-${i}`} className="hover:bg-[var(--surface-raised)]">
                  <td className="px-4 py-2 font-mono text-xs">{shortID(a.origin)}</td>
                  <td className="px-4 py-2 font-mono text-xs">{shortID(a.destination)}</td>
                  <td className="px-4 py-2">{a.transport}</td>
                  <td className="px-4 py-2 font-mono text-xs">{(a.path ?? []).map(shortID).join(" → ")}</td>
                  <td className="px-4 py-2 tabular">{formatBytes(a.rx_bytes)}</td>
                  <td className="px-4 py-2 tabular">{formatBytes(a.tx_bytes)}</td>
                  <td className="px-4 py-2 tabular">{a.rtt_ms?.toFixed(1)} ms</td>
                  <td className="px-4 py-2 tabular">{a.jitter_ms?.toFixed(1)} ms</td>
                </tr>
              ))}</tbody>
            </table></div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
