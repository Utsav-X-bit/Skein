import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { HistoryService, errText } from "@/lib/api";
import type { HistorySummary } from "../../../bindings/github.com/Utsav-X-bit/skein/gui/services/models";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { Input } from "@/components/ui/input";
import { Metric } from "@/components/ui/metric";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { formatBytes, shortID } from "@/lib/utils";
import { useLiveSampler, useMesh } from "@/features/mesh/useMesh";
import { bucketActivities, formatAge } from "@/features/mesh/model";
import { ThroughputStrip } from "@/features/mesh/ThroughputStrip";
import { useSessionEvents } from "@/features/notifications/store";
import { StoredChart, Timeline } from "@/features/activity/Timeline";
import { HistoryKey, historySummaryKey } from "@/features/activity/useHistoryRecorder";

/** Stored history is shown for one hour; the store keeps up to a day. */
const HistoryWindowMinutes = 60;
/** Link rows are aggregated so 500 raw samples stay readable. */
const BucketMs = 10_000;
/** Past two collection intervals the last link sample no longer describes now. */
const ExpiredAfterMs = 15_000;

function FilterChips({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-1" role="group" aria-label={`Filter by ${label}`}>
      <span className="mono-label mr-1">{label}</span>
      {options.map((option) => (
        <Button
          key={option.value}
          size="sm"
          variant={value === option.value ? "secondary" : "ghost"}
          aria-pressed={value === option.value}
          className="h-7 px-2 text-[11px]"
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </Button>
      ))}
    </div>
  );
}

export function Activity() {
  useLiveSampler(true);
  const mesh = useMesh();
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [confirmClear, setConfirmClear] = useState(false);
  const [exportPath, setExportPath] = useState<string | null>(null);

  const peerFilter = params.get("peer") ?? "";
  const directParam = params.get("direct");
  const directFilter = directParam === "direct" || directParam === "relay" ? directParam : "all";
  const transportFilter = params.get("transport") ?? "all";

  const setFilter = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next, { replace: true });
  };
  const resetFilters = () => setParams(new URLSearchParams(), { replace: true });
  const filtered = peerFilter !== "" || directFilter !== "all" || transportFilter !== "all";

  // History is GUI-local and opt-in. The summary is the only thing that says
  // whether recording is on; the store itself is the source of truth for it.
  // The same key is read by the app-wide writer in AppShell, so toggling here
  // starts and stops collection immediately.
  const summaryQ = useQuery({
    queryKey: historySummaryKey,
    queryFn: () => HistoryService.Summary(),
    staleTime: 30_000,
    // stored_points grows while recording; refresh so Export/Clear enable
    // without a reload or focus change.
    refetchInterval: (q) => (q.state.data?.enabled ? 30_000 : false),
    retry: 0,
  });
  const enabled = summaryQ.data?.enabled === true;

  const storedQ = useQuery({
    queryKey: [...HistoryKey, "samples", HistoryWindowMinutes],
    queryFn: () => HistoryService.Samples({ minutes: HistoryWindowMinutes }),
    staleTime: 25_000,
    // A recorded chart only needs the reconciliation cadence; the live ring
    // above it is what updates every 2 s, from the shared sampler.
    refetchInterval: enabled ? 30_000 : false,
    retry: 0,
  });

  const toggle = useMutation({
    mutationFn: (next: boolean) => HistoryService.SetEnabled(next),
    onSuccess: (_result, next) => {
      qc.setQueryData<HistorySummary | undefined>(historySummaryKey, (old) => (old ? { ...old, enabled: next } : old));
      void qc.invalidateQueries({ queryKey: historySummaryKey });
      void qc.invalidateQueries({ queryKey: [...HistoryKey, "samples"] });
      toast(next ? "Recording history on this device" : "History recording off");
    },
    onError: (e) => toast.error(errText(e)),
  });

  const clear = useMutation({
    mutationFn: () => HistoryService.Clear(),
    onSuccess: () => {
      setConfirmClear(false);
      void qc.invalidateQueries({ queryKey: HistoryKey });
      toast(`Stored history cleared. Recording stays ${enabled ? "on" : "off"}.`);
    },
    onError: (e) => toast.error(errText(e)),
  });

  const exportHistory = useMutation({
    mutationFn: (format: "json" | "csv") => HistoryService.Export(format),
    onSuccess: (path) => {
      setExportPath(path);
      toast.success(`Exported to ${path}`);
    },
    onError: (e) => {
      const text = errText(e);
      toast.error(/cancel/i.test(text) ? "Export cancelled" : text);
    },
  });

  const stored = useMemo(
    () => (storedQ.data ?? []).map((s) => ({ t: s.t, rx: s.rx_per_sec, tx: s.tx_per_sec })),
    [storedQ.data],
  );

  const buckets = useMemo(() => bucketActivities(mesh.activities, BucketMs), [mesh.activities]);
  const transports = useMemo(
    () => [...new Set(buckets.map((b) => b.transport).filter((t) => t !== ""))].sort(),
    [buckets],
  );
  const names = useMemo(() => new Map(mesh.peers.map((p) => [p.id, p.name || p.id])), [mesh.peers]);

  const needle = peerFilter.trim().toLowerCase();
  const rows = useMemo(() => {
    return buckets
      .filter((bucket) => {
        if (directFilter === "direct" && !bucket.direct) return false;
        if (directFilter === "relay" && bucket.direct) return false;
        if (transportFilter !== "all" && bucket.transport !== transportFilter) return false;
        if (needle) {
          const haystack = `${bucket.origin} ${bucket.destination} ${names.get(bucket.origin) ?? ""} ${names.get(bucket.destination) ?? ""}`;
          if (!haystack.toLowerCase().includes(needle)) return false;
        }
        return true;
      })
      .sort((a, b) => b.lastObservedMs - a.lastObservedMs);
  }, [buckets, directFilter, transportFilter, needle, names]);

  const events = useSessionEvents((s) => s.events);
  // The peer filter is page-level: a name resolves to its peer id so the
  // timeline narrows with the table instead of silently emptying.
  const visibleEvents = useMemo(() => {
    if (!needle) return events;
    const ids = mesh.peers
      .filter((p) => p.id.toLowerCase().includes(needle) || (p.name ?? "").toLowerCase().includes(needle))
      .map((p) => p.id);
    return events.filter((e) => e.peer_id !== undefined && ids.includes(e.peer_id));
  }, [events, needle, mesh.peers]);

  const now = Date.now();
  const storedPoints = summaryQ.data?.stored_points ?? 0;
  const canExport = storedPoints > 0 && !exportHistory.isPending;
  const label = (id: string) => names.get(id) ?? shortID(id);

  return (
    <div className="space-y-4">
      <div>
        <div className="mono-label">activity</div>
        <h1 className="mt-1 text-2xl font-medium tracking-tight">Activity</h1>
        <p className="mt-1 text-sm text-[var(--text-muted)]">
          Live link samples and lifecycle events from the daemon, plus optional history measured by this app on this device.
          Live evidence expires; recorded history keeps its gaps instead of drawing over them.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Recording</CardTitle>
          <Badge tone={enabled ? "accent" : "neutral"}>{enabled ? "on" : "off"}</Badge>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-start justify-between gap-6">
            <div className="min-w-0">
              <p className="text-sm text-[var(--text)]">Record aggregate throughput and peer counts on this device.</p>
              <p className="mt-1 max-w-2xl text-xs leading-relaxed text-[var(--text-muted)]">
                Samples hold per-second rates, peer counts and a timestamp — no peer names, addresses, endpoints, routes or payload
                contents. They are stored in this app's config directory, at a {summaryQ.data?.interval_s ?? 5} s interval, bounded by{" "}
                {summaryQ.data?.retain_mb ?? 50} MB. Collection runs only while this app is running; the daemon never records history.
              </p>
            </div>
            <Switch
              checked={enabled}
              disabled={toggle.isPending || summaryQ.isLoading}
              onCheckedChange={(next) => toggle.mutate(next)}
              aria-label="Record activity history on this device"
            />
          </div>

          {summaryQ.isError && (
            <p className="text-xs text-[var(--danger)]">History state unavailable: {errText(summaryQ.error)}</p>
          )}

          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Metric label="stored points" value={summaryQ.data ? String(storedPoints) : "—"} hint={`last ${HistoryWindowMinutes} min charted`} />
            <Metric label="stored size" value={summaryQ.data ? formatBytes(summaryQ.data.stored_bytes) : "—"} hint={`cap ${summaryQ.data?.retain_mb ?? 50} MB`} />
            <Metric label="interval" value={summaryQ.data ? `${summaryQ.data.interval_s} s` : "—"} />
            <Metric label="scope" value="this device" hint="GUI only, never the daemon" />
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Button variant="secondary" size="sm" disabled={!canExport} onClick={() => exportHistory.mutate("json")}>
              {exportHistory.isPending && exportHistory.variables === "json" ? "Exporting…" : "Export JSON"}
            </Button>
            <Button variant="secondary" size="sm" disabled={!canExport} onClick={() => exportHistory.mutate("csv")}>
              {exportHistory.isPending && exportHistory.variables === "csv" ? "Exporting…" : "Export CSV"}
            </Button>
            <Button variant="danger" size="sm" disabled={storedPoints === 0 || clear.isPending} onClick={() => setConfirmClear(true)}>
              Clear history
            </Button>
            {storedPoints === 0 && <span className="text-xs text-[var(--text-faint)]">Nothing stored yet.</span>}
          </div>

          {exportPath && (
            <p className="text-xs text-[var(--text-muted)]">
              Exported to <span className="break-all font-mono text-[var(--text)]">{exportPath}</span>
            </p>
          )}
        </CardContent>
      </Card>

      {storedQ.isError && (
        <p className="text-xs text-[var(--danger)]">Recorded samples unavailable: {errText(storedQ.error)}</p>
      )}

      <div className="grid gap-4 min-[1100px]:grid-cols-2">
        <ThroughputStrip />
        <StoredChart points={stored} intervalMs={(summaryQ.data?.interval_s ?? 5) * 1000} windowLabel={`last ${HistoryWindowMinutes} min`} />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Links</CardTitle>
          <span className="mono-label">
            {rows.length} of {buckets.length} · {BucketMs / 1000} s windows
          </span>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <Input
              value={peerFilter}
              onChange={(e) => setFilter("peer", e.target.value)}
              placeholder="Filter by peer id or name"
              aria-label="Filter links by peer"
              className="h-8 w-56 text-xs"
            />
            <FilterChips
              label="path"
              value={directFilter}
              onChange={(value) => setFilter("direct", value === "all" ? null : value)}
              options={[
                { value: "all", label: "All" },
                { value: "direct", label: "Direct" },
                { value: "relay", label: "Relayed" },
              ]}
            />
            {transports.length > 0 && (
              <FilterChips
                label="transport"
                value={transportFilter}
                onChange={(value) => setFilter("transport", value === "all" ? null : value)}
                options={[{ value: "all", label: "All" }, ...transports.map((t) => ({ value: t, label: t }))]}
              />
            )}
            {filtered && (
              <Button variant="ghost" size="sm" className="h-7 px-2 text-[11px]" onClick={resetFilters}>
                Reset filters
              </Button>
            )}
          </div>

          {mesh.loading && !mesh.status ? (
            <Skeleton className="h-32" />
          ) : mesh.error && !mesh.status ? (
            <ErrorState title="Cannot load activity" message={mesh.error} onRetry={mesh.refetch} />
          ) : rows.length === 0 ? (
            <EmptyState
              title={filtered ? "No samples match these filters" : "No link samples yet"}
              description={
                filtered
                  ? "The daemon has no recent link samples for this peer, path type or transport."
                  : "Samples appear when peers exchange traffic. Each row is an aggregated window, not a rate."
              }
              className="m-0"
              action={filtered ? <Button variant="secondary" size="sm" onClick={resetFilters}>Reset filters</Button> : undefined}
            />
          ) : (
            <>
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm">
                  <thead className="border-b border-[var(--border)] text-[var(--text-faint)]">
                    <tr>
                      {["Origin → destination", "Transport", "Path", "RX Δ", "TX Δ", "RTT", "Jitter", "Observed", "Samples"].map((heading) => (
                        <th key={heading} className="mono-label px-3 py-2 font-normal">{heading}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[var(--border)]">
                    {rows.map((bucket) => {
                      const ageMs = now - bucket.lastObservedMs;
                      const expired = ageMs > ExpiredAfterMs;
                      return (
                        <tr key={bucket.key} className={expired ? "text-[var(--text-muted)]" : "hover:bg-[var(--surface-raised)]"}>
                          <td className="px-3 py-2">
                            <span className="font-mono text-xs">{label(bucket.origin)}</span>
                            <span className="px-1 text-[var(--text-faint)]">→</span>
                            <span className="font-mono text-xs">{label(bucket.destination)}</span>
                          </td>
                          <td className="px-3 py-2">
                            <span className="inline-flex items-center gap-1">
                              {bucket.transport || "—"}
                              <Badge tone={bucket.direct ? "success" : "relay"}>{bucket.direct ? "direct" : "relayed"}</Badge>
                            </span>
                          </td>
                          <td className="px-3 py-2 font-mono text-xs">{(bucket.path ?? []).length > 0 ? bucket.path.map(shortID).join(" → ") : "—"}</td>
                          <td className="px-3 py-2 tabular">{formatBytes(bucket.rx)}</td>
                          <td className="px-3 py-2 tabular">{formatBytes(bucket.tx)}</td>
                          <td className="px-3 py-2 tabular">{bucket.rttMs > 0 ? `${bucket.rttMs.toFixed(1)} ms` : "—"}</td>
                          <td className="px-3 py-2 tabular">{bucket.jitterMs > 0 ? `${bucket.jitterMs.toFixed(1)} ms` : "—"}</td>
                          <td className="px-3 py-2">
                            <span className="inline-flex items-center gap-2">
                              <span className="tabular text-[var(--text-muted)]">{formatAge(ageMs)} ago</span>
                              {expired && <Badge tone="warning">expired</Badge>}
                            </span>
                          </td>
                          <td className="px-3 py-2 tabular text-[var(--text-muted)]">{bucket.samples}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              <p className="text-[11px] leading-relaxed text-[var(--text-faint)]">
                RX/TX are byte deltas over the daemon's unexported window — not rates, so no per-second figure is derived from them.
                Expired rows stay visible, labelled, until they fall out of the bounded sample buffer.
              </p>
            </>
          )}
        </CardContent>
      </Card>

      <Timeline events={visibleEvents} />

      <Dialog open={confirmClear} onOpenChange={setConfirmClear}>
        <DialogContent>
          <DialogTitle>Clear recorded history?</DialogTitle>
          <DialogDescription>
            This deletes every stored sample on this device. The recording setting is kept
            {enabled ? " on, so collection resumes with the next measured sample." : " off, so nothing is collected until you turn it back on."}
            {" "}Nothing is deleted on the daemon — it never held this history.
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setConfirmClear(false)}>Cancel</Button>
            <Button variant="danger" onClick={() => clear.mutate()} disabled={clear.isPending}>
              {clear.isPending ? "Clearing…" : "Clear history"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
