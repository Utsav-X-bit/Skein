import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { ClipboardCopy, Download, Network as NetworkIcon, Pause, Play, Radar, RefreshCw, Route as RouteIcon, Search } from "lucide-react";
import { toast } from "sonner";
import { DiagnosticsService, NetworkService, PeerService, errText, type LogEntry } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorState } from "@/components/ui/error-state";
import { Input, Label } from "@/components/ui/input";
import { formatAge } from "@/features/mesh/model";
import { useLive } from "@/features/mesh/useDaemonEvents";
import { cn, shortID } from "@/lib/utils";
import { LogLevels, LogWindowStep, filterLogs, logKey, logLine, mergeLogs, type LogLevel } from "./model";

const levelTone: Record<string, string> = {
  error: "text-[var(--danger)]",
  warning: "text-[var(--warning)]",
};

/**
 * Diagnostics. The log list is the daemon's retained ring (from the support
 * bundle, which is the complete redacted snapshot) merged with the live
 * `log:entry` stream. It renders a bounded window, not the whole ring, so a
 * burst cannot lock the window up.
 */
export function Logs() {
  const [params] = useSearchParams();
  const qParam = params.get("q") ?? "";
  const [q, setQ] = useState(qParam);
  const [level, setLevel] = useState<LogLevel>("all");
  const [paused, setPaused] = useState(false);
  const [pausedAt, setPausedAt] = useState<number | null>(null);
  const [frozen, setFrozen] = useState<LogEntry[] | null>(null);
  const [windowSize, setWindowSize] = useState(LogWindowStep);
  const [selected, setSelected] = useState<string | null>(null);
  const [follow, setFollow] = useState(true);
  const [bundlePath, setBundlePath] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // A palette jump lands here with ?q=; typing keeps the URL untouched so the
  // filter is not written on every keystroke.
  useEffect(() => setQ(qParam), [qParam]);
  useEffect(() => {
    setWindowSize(LogWindowStep);
    setSelected(null);
  }, [level, q]);

  const live = useLive((s) => s.logs);
  const bundle = useQuery({ queryKey: ["diagnostics", "bundle"], queryFn: () => DiagnosticsService.Bundle(), staleTime: 30_000, retry: 0 });
  const peers = useQuery({ queryKey: ["peers"], queryFn: () => PeerService.List(), staleTime: 5_000, retry: 0 });
  const routes = useQuery({ queryKey: ["routes"], queryFn: () => NetworkService.Routes(), staleTime: 5_000, retry: 0 });

  const merged = useMemo(() => mergeLogs(bundle.data?.logs, live), [bundle.data, live]);
  const source = paused && frozen ? frozen : merged;
  const rows = useMemo(() => filterLogs(source, level, q), [source, level, q]);
  const visible = rows.slice(-windowSize);
  const counts = useMemo(() => {
    const scoped = filterLogs(source, "all", q);
    const out: Record<LogLevel, number> = { all: scoped.length, info: 0, warning: 0, error: 0 };
    for (const row of scoped) {
      if (row.level === "info" || row.level === "warning" || row.level === "error") out[row.level] += 1;
    }
    return out;
  }, [source, q]);

  const activeIndex = visible.findIndex((row) => logKey(row) === selected);
  const activeRow = activeIndex >= 0 ? visible[activeIndex] : null;
  // The window stays at a fixed length once it fills, so following must key on
  // the newest line, not on the row count.
  const newestKey = visible.length > 0 ? logKey(visible[visible.length - 1]) : "";

  // Follow only while the newest line is on screen; scrolling up stops it
  // instead of yanking the view back on the next event.
  useEffect(() => {
    if (!follow || paused) return;
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [newestKey, follow, paused, level, q]);

  const copyLine = async (row: LogEntry) => {
    try {
      await navigator.clipboard.writeText(logLine(row));
      toast.success("Log line copied");
    } catch {
      toast.error("The clipboard is not available; select the text and copy it manually.");
    }
  };

  const exportBundle = useMutation({
    mutationFn: () => DiagnosticsService.ExportBundle(),
    onSuccess: (path) => {
      setBundlePath(path);
      toast.success("Diagnostic bundle saved", { description: path });
    },
    onError: (e) => {
      const text = errText(e);
      if (/cancel/i.test(text)) toast("Diagnostic bundle export cancelled");
      else toast.error(text);
    },
  });

  const move = (delta: number) => {
    if (visible.length === 0) return;
    const base = activeIndex < 0 ? (delta > 0 ? -1 : visible.length) : activeIndex;
    const next = Math.min(visible.length - 1, Math.max(0, base + delta));
    setSelected(logKey(visible[next]));
  };

  const onListKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      move(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      move(-1);
    } else if (e.key === "Home") {
      e.preventDefault();
      if (visible.length) setSelected(logKey(visible[0]));
    } else if (e.key === "End") {
      e.preventDefault();
      if (visible.length) setSelected(logKey(visible[visible.length - 1]));
    } else if (e.key === "Enter" && activeRow) {
      e.preventDefault();
      void copyLine(activeRow);
    }
  };

  const togglePause = () => {
    if (paused) {
      setPaused(false);
      setFrozen(null);
      setPausedAt(null);
      setFollow(true);
    } else {
      setFrozen(merged);
      setPaused(true);
      setPausedAt(Date.now());
    }
  };

  // --- troubleshooter -------------------------------------------------------
  const [peerId, setPeerId] = useState("");
  const peer = (peers.data ?? []).find((p) => p.id === peerId) ?? null;
  const liveProbe = useLive((s) => (peerId ? s.probes[peerId] : undefined));
  const probe = useMutation({ mutationFn: (id: string) => PeerService.Probe(id), onError: (e) => toast.error(errText(e)) });
  const ping = useMutation({ mutationFn: (ip: string) => NetworkService.Ping(ip), onError: (e) => toast.error(errText(e)) });
  const probeResult = probe.data ?? liveProbe;
  const peerRoutes = (routes.data ?? []).filter((route) => route.destination === peerId);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-medium tracking-tight">Diagnostics</h1>
          <p className="text-sm text-[var(--text-muted)]">
            The daemon&apos;s redacted log tail, a support bundle you can save, and per-peer checks. Secrets and join strings never leave the daemon.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" onClick={() => void bundle.refetch()} disabled={bundle.isFetching}>
            <RefreshCw size={15} className={bundle.isFetching ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button onClick={() => exportBundle.mutate()} disabled={exportBundle.isPending}>
            <Download size={15} /> {exportBundle.isPending ? "Saving…" : "Save diagnostic bundle"}
          </Button>
        </div>
      </div>

      {bundle.isError && (
        <ErrorState
          title="Cannot load the diagnostic bundle"
          message={errText(bundle.error)}
          remediation="The live tail below still updates from daemon events. Start the service to read the retained log."
          onRetry={() => void bundle.refetch()}
        />
      )}
      {bundlePath && (
        <p className="text-xs text-[var(--text-muted)]">
          Saved to <span className="break-all font-mono text-[var(--text)]">{bundlePath}</span>
        </p>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Daemon log</CardTitle>
          <span className="mono-label">
            {paused && pausedAt ? `paused at ${new Date(pausedAt).toLocaleTimeString()}` : "live tail"}
          </span>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex min-w-[180px] flex-1 items-center gap-2 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3">
              <Search size={14} className="shrink-0 text-[var(--text-faint)]" aria-hidden />
              <Input
                className="border-0 bg-transparent px-0 focus-visible:ring-0"
                placeholder="Filter log lines…"
                aria-label="Filter log lines"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </div>
            <div className="flex flex-wrap items-center gap-1" role="group" aria-label="Log level">
              {LogLevels.map((option) => (
                <Button
                  key={option.value}
                  size="sm"
                  variant={level === option.value ? "default" : "ghost"}
                  aria-pressed={level === option.value}
                  onClick={() => setLevel(option.value)}
                >
                  {option.label} <span className="tabular opacity-70">{counts[option.value]}</span>
                </Button>
              ))}
            </div>
            <Button size="sm" variant="secondary" aria-pressed={paused} onClick={togglePause}>
              {paused ? <Play size={14} /> : <Pause size={14} />} {paused ? "Resume" : "Pause"}
            </Button>
            <Button size="sm" variant="secondary" aria-pressed={follow} disabled={paused} onClick={() => setFollow((value) => !value)}>
              Follow
            </Button>
            <Button size="sm" variant="secondary" disabled={!activeRow} onClick={() => activeRow && void copyLine(activeRow)}>
              <ClipboardCopy size={14} /> Copy line
            </Button>
          </div>

          <div
            ref={listRef}
            onScroll={() => {
              const el = listRef.current;
              if (!el) return;
              const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
              if (atBottom !== follow) setFollow(atBottom);
            }}
            className="max-h-[52vh] overflow-auto rounded-md border border-[var(--border)] font-mono text-xs"
          >
            {visible.length === 0 ? (
              <p className="px-4 py-8 text-center text-[var(--text-faint)]">
                {paused ? "Paused — no retained line matches these filters." : "No retained log line matches these filters."}
              </p>
            ) : (
              <div
                role="listbox"
                aria-label="Daemon log tail"
                aria-activedescendant={activeIndex >= 0 ? `log-option-${activeIndex}` : undefined}
                tabIndex={0}
                onKeyDown={onListKeyDown}
                className="focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)]"
              >
                {visible.map((row, index) => (
                  <div
                    key={logKey(row)}
                    id={`log-option-${index}`}
                    role="option"
                    aria-selected={index === activeIndex}
                    onMouseDown={() => setSelected(logKey(row))}
                    onDoubleClick={() => void copyLine(row)}
                    className={cn("flex gap-3 border-b border-[var(--border)] px-4 py-1.5 last:border-b-0", index === activeIndex && "bg-[var(--surface-raised)]")}
                  >
                    <span className="tabular shrink-0 text-[var(--text-faint)]">{new Date(row.time_ms).toLocaleTimeString()}</span>
                    <span className={cn("w-16 shrink-0 uppercase", levelTone[row.level] ?? "text-[var(--text-faint)]")}>{row.level}</span>
                    <span className="min-w-0 flex-1 break-words text-[var(--text-muted)]">{row.message}</span>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-[var(--text-faint)]">
            <span>
              {visible.length} of {rows.length} matching · {source.length} retained
              {paused ? " · new lines are held" : " · live events + the bundle's retained ring"}
            </span>
            <span className="flex items-center gap-2">
              {rows.length > visible.length && (
                <Button size="sm" variant="secondary" onClick={() => setWindowSize((size) => size + LogWindowStep)}>
                  Load older lines
                </Button>
              )}
              <span className="font-mono">↑↓ select · ↵ copy</span>
            </span>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Support bundle</CardTitle>
          <span className="mono-label">redacted</span>
        </CardHeader>
        <CardContent className="space-y-2 text-xs text-[var(--text-muted)]">
          <p>
            The bundle is the same JSON this page reads: log tail, peer and route snapshots, the redacted configuration view, platform and protocol versions. It is written 0600 to the path you choose.
          </p>
          <p>
            Redactions applied by the daemon:{" "}
            <span className="font-mono text-[var(--text)]">
              {bundle.data?.redactions && bundle.data.redactions.length > 0 ? bundle.data.redactions.join(", ") : bundle.data ? "none reported" : "—"}
            </span>
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Troubleshooter</CardTitle>
          <span className="mono-label">observed facts only</span>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs leading-relaxed text-[var(--text-muted)]">
            Pick a peer and run the daemon&apos;s own checks. A missing route or an unreachable ping is reported as observed; nothing here is inferred to be healthy.
          </p>
          <div className="flex flex-wrap items-end gap-2">
            <div className="min-w-[220px] flex-1">
              <Label htmlFor="troubleshoot-peer">Peer</Label>
              <select
                id="troubleshoot-peer"
                value={peerId}
                onChange={(e) => {
                  setPeerId(e.target.value);
                  probe.reset();
                  ping.reset();
                }}
                className="h-10 w-full rounded-md border border-[var(--border-strong)] bg-[var(--surface)] px-3 text-sm text-[var(--text)]"
              >
                <option value="">Select a peer…</option>
                {(peers.data ?? []).map((option) => (
                  <option key={option.id} value={option.id}>
                    {option.name || shortID(option.id)} — {option.overlay_ipv4 || "no overlay address"}
                  </option>
                ))}
              </select>
            </div>
            <Button size="sm" variant="secondary" disabled={!peer || probe.isPending} onClick={() => peer && probe.mutate(peer.id)}>
              <Radar size={14} /> {probe.isPending ? "Probing…" : "Probe"}
            </Button>
            <Button
              size="sm"
              variant="secondary"
              disabled={!peer?.overlay_ipv4 || ping.isPending}
              title={peer && !peer.overlay_ipv4 ? "The daemon reports no overlay address for this peer" : undefined}
              onClick={() => peer?.overlay_ipv4 && ping.mutate(peer.overlay_ipv4)}
            >
              <NetworkIcon size={14} /> {ping.isPending ? "Pinging…" : "Ping"}
            </Button>
            <Button size="sm" variant="secondary" disabled={routes.isFetching} onClick={() => void routes.refetch()}>
              <RouteIcon size={14} /> Refresh routes
            </Button>
          </div>

          {!peers.isLoading && (peers.data ?? []).length === 0 && (
            <p className="text-xs text-[var(--text-faint)]">No peer is known to the daemon, so there is nothing to check.</p>
          )}
          {peers.isError && <ErrorState title="Cannot list peers" message={errText(peers.error)} onRetry={() => void peers.refetch()} />}

          {peer && (
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1 rounded-md border border-[var(--border)] p-3 text-xs">
                <div className="mono-label">peer</div>
                <div className="flex flex-wrap items-center gap-2">
                  <Badge tone={peer.state === "up" ? "success" : peer.state === "degraded" ? "warning" : "neutral"}>{peer.state}</Badge>
                  <Badge tone={peer.direct ? "accent" : "relay"}>{peer.direct ? "direct" : "relay"}</Badge>
                  <span className="font-mono text-[var(--text-faint)]">{shortID(peer.id)}</span>
                </div>
                <div>overlay: {peer.overlay_ipv4 || "not reported"}</div>
                <div>transport: {peer.transport || "unknown"}</div>
                <div>last seen: {peer.last_seen_ms > 0 ? `${formatAge(peer.last_seen_ms)} ago` : "unknown"}</div>
              </div>
              <div className="space-y-1 rounded-md border border-[var(--border)] p-3 text-xs">
                <div className="mono-label">routes to this peer</div>
                {peerRoutes.length === 0 ? (
                  <p className="text-[var(--text-muted)]">No route to this peer is reported by the daemon.</p>
                ) : (
                  <ul className="space-y-1">
                    {peerRoutes.map((route) => (
                      <li key={`${route.destination}-${route.next_hop}`}>
                        <span className="font-mono">{route.path?.join(" → ") || shortID(route.next_hop)}</span>
                        <span className="tabular text-[var(--text-muted)]"> · {route.hops} hops · {route.cost_ms.toFixed(1)} ms</span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          )}

          {probeResult && (
            <div className="rounded-md border border-[var(--border)] p-2 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <span className="mono-label">probe</span>
                <Badge tone={probeResult.state === "up" ? "success" : probeResult.state === "degraded" ? "warning" : "danger"}>{probeResult.state}</Badge>
                {probeResult.rtt_ms !== undefined && probeResult.rtt_ms > 0 && <span className="tabular">{probeResult.rtt_ms.toFixed(1)} ms</span>}
              </div>
              {probeResult.error && <div className="mt-1 break-words text-[var(--danger)]">{probeResult.error}</div>}
            </div>
          )}

          {ping.data && (
            <div className="rounded-md border border-[var(--border)] p-2 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <span className="mono-label">ping</span>
                <Badge tone={ping.data.reachable ? "success" : "danger"}>{ping.data.reachable ? "reachable" : "unreachable"}</Badge>
                <span className="tabular text-[var(--text-muted)]">exit {ping.data.exit_code}</span>
                <span className="font-mono text-[var(--text-faint)]">{ping.data.address}</span>
              </div>
              <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] text-[var(--text-muted)]">
                {ping.data.output || "(no output)"}
              </pre>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
