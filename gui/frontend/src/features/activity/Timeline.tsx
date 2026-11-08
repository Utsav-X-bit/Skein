import { Link } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { shortID, formatDuration } from "@/lib/utils";
import type { SessionEvent } from "@/features/notifications/store";
import { segments } from "@/features/mesh/ThroughputStrip";
import { formatBytesPerSec } from "@/features/mesh/model";

/** StoredPoint is a recorded sample flattened from the history binding's wire shape. */
export type StoredPoint = { t: number; rx: number; tx: number };

type Series = (number | null)[];

/**
 * storedSeries turns recorded points into a chart series. A hole longer than
 * two collection intervals becomes null so the line breaks: the app was closed
 * or recording was off, and nothing was measured in between.
 */
export function storedSeries(points: StoredPoint[], intervalMs: number, pick: "rx" | "tx"): Series {
  const out: Series = [];
  points.forEach((point, i) => {
    if (i > 0 && point.t - points[i - 1].t > 2 * intervalMs) out.push(null);
    out.push(point[pick]);
  });
  return out;
}

/** gapSpans counts the collection holes and how long they lasted in total. */
export function gapSpans(points: StoredPoint[], intervalMs: number): { count: number; ms: number } {
  let count = 0;
  let ms = 0;
  for (let i = 1; i < points.length; i++) {
    const hole = points[i].t - points[i - 1].t - intervalMs;
    if (hole > intervalMs) {
      count += 1;
      ms += hole;
    }
  }
  return { count, ms };
}

const width = 600;
const height = 90;

/**
 * StoredChart is the recorded-history chart. It is a separate series from the
 * live ring because it is a separate claim: measured by this app on this
 * device, at the collection interval, with the holes left visible.
 */
export function StoredChart({
  points,
  intervalMs,
  windowLabel,
}: {
  points: StoredPoint[];
  intervalMs: number;
  windowLabel: string;
}) {
  const rx = storedSeries(points, intervalMs, "rx");
  const tx = storedSeries(points, intervalMs, "tx");
  const gaps = gapSpans(points, intervalMs);
  const max = Math.max(
    1,
    ...rx.filter((v): v is number => v !== null),
    ...tx.filter((v): v is number => v !== null),
  );
  const rxPaths = segments(rx, width, height, max);
  const txPaths = segments(tx, width, height, max);
  const last = points[points.length - 1];

  return (
    <Card>
      <CardHeader>
        <CardTitle>Recorded history</CardTitle>
        <span className="mono-label">this app, this device · {windowLabel}</span>
      </CardHeader>
      <CardContent>
        {points.length < 2 ? (
          <div className="py-6 text-center text-xs text-[var(--text-muted)]">
            {points.length === 0
              ? "No stored samples. Recording is opt-in; nothing is collected until it is on."
              : "One stored sample — a rate needs two counter samples."}
          </div>
        ) : (
          <>
            <svg
              viewBox={`0 0 ${width} ${height}`}
              className="h-auto w-full"
              role="img"
              aria-label={`Recorded throughput for ${windowLabel}, ${points.length} samples, ${gaps.count} collection gaps`}
            >
              {rxPaths.map((points, i) => (
                <polyline key={`rx-${i}`} points={points} fill="none" stroke="var(--accent)" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round" />
              ))}
              {txPaths.map((points, i) => (
                <polyline key={`tx-${i}`} points={points} fill="none" stroke="var(--relay)" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round" />
              ))}
            </svg>
            <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-[var(--text-muted)]">
              <span className="tabular">↓ {formatBytesPerSec(last.rx)} rx</span>
              <span className="tabular">↑ {formatBytesPerSec(last.tx)} tx</span>
              <span className="tabular">
                {points.length} points · {Math.round(intervalMs / 1000)} s interval
              </span>
              <span className="ml-auto inline-flex items-center gap-3">
                <span className="inline-flex items-center gap-1"><span className="inline-block h-0.5 w-4 bg-[var(--accent)]" /> rx</span>
                <span className="inline-flex items-center gap-1"><span className="inline-block h-0.5 w-4 bg-[var(--relay)]" /> tx</span>
              </span>
            </div>
            <p className="mt-2 text-[11px] leading-relaxed text-[var(--text-faint)]">
              {gaps.count === 0
                ? "No collection gaps in this window. Lines break where nothing was measured, never at zero."
                : `${gaps.count} collection gap${gaps.count === 1 ? "" : "s"} totalling ${formatDuration(gaps.ms / 1000)} — the app was closed or recording was off. Lines break where nothing was measured.`}
            </p>
          </>
        )}
      </CardContent>
    </Card>
  );
}

const labels: Record<string, string> = {
  peer_up: "Peer up",
  peer_down: "Peer down",
  peer_joined: "Peer joined",
  peer_discovered: "Peer discovered",
  peer_added: "Peer added",
  peer_removed: "Peer removed",
  peer_departed: "Peer departed",
  dialer_fallback: "Dialer fallback",
  "peer:leave": "Peer left the network",
  "peer:rejoined": "Peer rejoined the network",
  name_changed: "Node name changed",
  secret_rotated: "Network secret rotated",
  system_reset: "Configuration reset",
  system_imported: "Configuration imported",
  leave_dataplane_stop_failed: "Leave: dataplane stop failed",
};

function deepLink(type: string, peerId?: string): string | null {
  if (peerId) return `/peers?peer=${encodeURIComponent(peerId)}`;
  if (type === "system_reset" || type === "system_imported" || type === "secret_rotated") return "/settings";
  return null;
}

/**
 * Timeline is the session event feed: lifecycle events this app received,
 * newest first. It is in memory only and is not history — a restart clears it.
 */
export function Timeline({ events }: { events: SessionEvent[] }) {
  const recent = [...events].reverse();
  return (
    <Card>
      <CardHeader>
        <CardTitle>Session timeline</CardTitle>
        <span className="mono-label">this session · in memory · not recorded</span>
      </CardHeader>
      <CardContent className="p-0">
        {recent.length === 0 ? (
          <div className="px-4 py-6 text-center text-xs text-[var(--text-muted)]">No lifecycle events this session.</div>
        ) : (
          <ol className="divide-y divide-[var(--border)]">
            {recent.map((ev) => {
              const label = labels[ev.type] ?? ev.type;
              const to = deepLink(ev.type, ev.peer_id);
              return (
                <li key={ev.key} className="flex items-baseline gap-3 px-4 py-2 text-xs">
                  <time className="tabular shrink-0 text-[var(--text-faint)]" dateTime={new Date(ev.time_ms).toISOString()}>
                    {new Date(ev.time_ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}
                  </time>
                  <span className="min-w-0 flex-1 truncate">
                    {to ? (
                      <Link to={to} className="text-[var(--text)] underline-offset-4 hover:underline">{label}</Link>
                    ) : (
                      <span className="text-[var(--text)]">{label}</span>
                    )}
                    {ev.detail && <span className="ml-2 text-[var(--text-faint)]">{ev.detail}</span>}
                  </span>
                  {ev.peer_id && <span className="shrink-0 font-mono text-[var(--text-faint)]">{shortID(ev.peer_id)}</span>}
                </li>
              );
            })}
          </ol>
        )}
      </CardContent>
    </Card>
  );
}
