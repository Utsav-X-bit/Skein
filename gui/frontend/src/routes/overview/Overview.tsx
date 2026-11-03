import { useQuery } from "@tanstack/react-query";
import { ArrowDownRight, ArrowUpRight, Link2, Users } from "lucide-react";
import { NetworkService, PeerService, errText, isOffline } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Metric } from "@/components/ui/metric";
import { Sparkline } from "@/components/ui/sparkline";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { Skeleton } from "@/components/ui/skeleton";
import { Badge } from "@/components/ui/badge";
import { StatusDot } from "@/components/ui/status-dot";
import { formatBytes, formatDuration, shortID } from "@/lib/utils";
import { Link } from "react-router-dom";

export function Overview() {
  const status = useQuery({ queryKey: ["status"], queryFn: () => NetworkService.Status(), refetchInterval: 2_000 });
  const peers = useQuery({ queryKey: ["peers"], queryFn: () => PeerService.List(), refetchInterval: 2_000 });
  const activities = useQuery({ queryKey: ["activities"], queryFn: () => NetworkService.Activities(), refetchInterval: 2_000 });


  if (status.isLoading) return <div className="grid gap-4 md:grid-cols-2">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-28" />)}</div>;
  if (status.isError) {
    return <ErrorState title="Daemon offline" message={errText(status.error)} remediation="Start the Skein service or install it from Settings." onRetry={() => status.refetch()} />;
  }
  const st = status.data!;
  const ps = peers.data ?? [];
  const up = ps.filter((p) => p.state === "up").length;
  const direct = ps.filter((p) => p.direct).length;
  const relay = ps.length - direct;
  const samples = (activities.data ?? []).slice(-60).map((a) => (a.rx_bytes ?? 0) + (a.tx_bytes ?? 0));
  const connected = st.connection?.state === "connected";

  return (
    <div className="space-y-6">
      <section className="relative overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--surface)] p-6">
        <div className="pointer-events-none absolute inset-0 grid-bg opacity-30" />
        <div className="relative flex flex-wrap items-end justify-between gap-6">
          <div>
            <div className="mono-label">mesh status</div>
            <h1 className="mt-2 text-3xl font-medium tracking-tight">{connected ? "Connected" : st.connection?.state === "paused" ? "Paused" : "Connecting"}</h1>
            <div className="mt-2 flex flex-wrap items-center gap-3 text-sm text-[var(--text-muted)]">
              <StatusDot tone={connected ? "up" : st.connection?.state === "paused" ? "paused" : "down"} pulse={connected} />
              <span>{st.overlay_ipv4}{st.overlay_ipv6 ? ` · ${st.overlay_ipv6}` : ""}</span>
              <span>·</span>
              <span>up {formatDuration(st.uptime_seconds)}</span>
              {st.connection?.last_error && <Badge tone="danger">{st.connection.last_error}</Badge>}
            </div>
          </div>
          <div className="grid grid-cols-2 gap-6 sm:grid-cols-4">
            <Metric label="peers" value={`${up}/${ps.length}`} hint="up / total" />
            <Metric label="direct" value={`${direct}`} hint={`${relay} relayed`} />
            <Metric label="down" value={formatBytes(st.rx_bytes)} hint="received" />
            <Metric label="up" value={formatBytes(st.tx_bytes)} hint="sent" />
          </div>
        </div>
      </section>

      <div className="grid gap-4 lg:grid-cols-[1.4fr_1fr]">
        <Card>
          <CardHeader><CardTitle>Throughput</CardTitle><span className="mono-label">last 60 samples</span></CardHeader>
          <CardContent>
            {samples.length > 1 ? <Sparkline values={samples} width={640} height={120} /> : <EmptyState title="No samples yet" description="Traffic appears here once peers exchange packets." />}
            <div className="mt-3 flex gap-6 text-xs text-[var(--text-muted)]">
              <span className="inline-flex items-center gap-1"><ArrowDownRight size={13} /> {formatBytes(st.rx_bytes)} total</span>
              <span className="inline-flex items-center gap-1"><ArrowUpRight size={13} /> {formatBytes(st.tx_bytes)} total</span>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>Peers</CardTitle><Link to="/peers" className="text-xs text-[var(--accent)]">View all</Link></CardHeader>
          <CardContent className="space-y-2">
            {ps.length === 0 ? <EmptyState title="No peers yet" description="Invite a device to join this mesh." /> : ps.slice(0, 6).map((p) => (
              <div key={p.id} className="flex items-center justify-between gap-3 rounded-md border border-[var(--border)] px-3 py-2">
                <div className="flex min-w-0 items-center gap-2"><StatusDot tone={p.state === "up" ? "up" : "down"} /><span className="truncate text-sm">{p.name || shortID(p.id)}</span></div>
                <div className="flex items-center gap-2 text-xs text-[var(--text-muted)]"><Link2 size={12} />{p.direct ? "direct" : "relay"}{p.rtt_ms ? ` · ${p.rtt_ms.toFixed(1)} ms` : ""}</div>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
