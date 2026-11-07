import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { formatBytesPerSec, StateInterval, type HistorySample } from "./model";
import { useSessionHistory } from "./useSessionHistory";

const Slots = 60;

type Series = (number | null)[];

// A missing sample is a gap, not zero: the sampler was hidden or the daemon
// was unreachable. Inserting null keeps the line broken instead of drawing
// traffic that was never measured.
function withGaps(samples: HistorySample[], pick: (s: HistorySample) => number): Series {
  const recent = samples.slice(-Slots);
  const out: Series = [];
  for (let i = 0; i < recent.length; i++) {
    if (i > 0 && recent[i].t - recent[i - 1].t > 2 * StateInterval) out.push(null);
    out.push(pick(recent[i]));
  }
  return out;
}

function segments(values: Series, width: number, height: number, max: number): string[] {
  const step = values.length > 1 ? width / (values.length - 1) : width;
  const out: string[] = [];
  let current: string[] = [];
  values.forEach((value, i) => {
    if (value === null) {
      if (current.length > 0) out.push(current.join(" "));
      current = [];
      return;
    }
    const x = i * step;
    const y = height - (value / max) * (height - 6) - 3;
    current.push(`${x.toFixed(1)},${y.toFixed(1)}`);
  });
  if (current.length > 0) out.push(current.join(" "));
  return out;
}

/** ThroughputStrip is the session ring: measured rates only, gaps preserved. */
export function ThroughputStrip() {
  const samples = useSessionHistory();
  const recent = samples.slice(-Slots);
  const last = recent[recent.length - 1];
  const rx = withGaps(recent, (s) => s.rxPerSec);
  const tx = withGaps(recent, (s) => s.txPerSec);
  const max = Math.max(1, ...rx.filter((v): v is number => v !== null), ...tx.filter((v): v is number => v !== null));
  const width = 600;
  const height = 90;
  const rxPaths = segments(rx, width, height, max);
  const txPaths = segments(tx, width, height, max);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Throughput</CardTitle>
        <span className="mono-label">last 60 samples · 2 s</span>
      </CardHeader>
      <CardContent>
        {recent.length === 0 ? (
          <div className="py-6 text-center text-xs text-[var(--text-muted)]">
            No throughput samples yet. Rates appear once two counter samples share a window.
          </div>
        ) : (
          <svg
            viewBox={`0 0 ${width} ${height}`}
            className="h-auto w-full"
            role="img"
            aria-label={`Throughput over the last ${recent.length} samples, gaps preserved`}
          >
            {rxPaths.map((points, i) => (
              <polyline key={`rx-${i}`} points={points} fill="none" stroke="var(--accent)" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round" />
            ))}
            {txPaths.map((points, i) => (
              <polyline key={`tx-${i}`} points={points} fill="none" stroke="var(--relay)" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round" />
            ))}
          </svg>
        )}
        <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-[var(--text-muted)]">
          <span className="tabular">↓ {last ? formatBytesPerSec(last.rxPerSec) : "—"} rx</span>
          <span className="tabular">↑ {last ? formatBytesPerSec(last.txPerSec) : "—"} tx</span>
          <span className="ml-auto inline-flex items-center gap-3">
            <span className="inline-flex items-center gap-1"><span className="inline-block h-0.5 w-4 bg-[var(--accent)]" /> rx</span>
            <span className="inline-flex items-center gap-1"><span className="inline-block h-0.5 w-4 bg-[var(--relay)]" /> tx</span>
          </span>
        </div>
      </CardContent>
    </Card>
  );
}
