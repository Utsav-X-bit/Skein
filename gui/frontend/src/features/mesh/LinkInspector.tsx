import { ArrowDownRight, ArrowUpRight } from "lucide-react";
import type { Activity, Peer } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { formatBytes, shortID } from "@/lib/utils";
import { InspectorFrame } from "./PeerInspector";
import {
  activityWindow,
  formatAge,
  formatBytesPerSec,
  routeSummary,
  type MeshModel,
} from "./model";

/** LinkInspector explains the evidence behind one edge. */
export function LinkInspector({
  link,
  model,
  peers,
  activities,
  onClose,
}: {
  link: { from: string; to: string } | null;
  model: MeshModel;
  peers: Peer[];
  activities: Activity[];
  onClose: () => void;
}) {
  if (!link) return null;

  const self = model.nodes.find((n) => n.kind === "self")?.id ?? "";
  const edge = model.edges.find(
    (e) => (e.from === link.from && e.to === link.to) || (e.from === link.to && e.to === link.from),
  );
  const rows = activities
    .filter(
      (a) =>
        (a.origin === link.from && a.destination === link.to) ||
        (a.origin === link.to && a.destination === link.from),
    )
    .sort((a, b) => a.observed_ms - b.observed_ms || a.seq - b.seq);
  const latest = rows[rows.length - 1];
  const prev = latest
    ? rows.find((r) => r.origin === latest.origin && r.destination === latest.destination && r.seq === latest.seq - 1) ?? null
    : null;
  const window = latest ? activityWindow(prev, latest) : null;
  const owning = model.paths.find((p) => {
    const chain = p.path.length > 0 ? [self, ...p.path] : [self, p.destination];
    for (let i = 0; i < chain.length - 1; i++) {
      if (chain[i] === link.from && chain[i + 1] === link.to) return true;
    }
    return false;
  });
  const nameOf = (id: string) => peers.find((p) => p.id === id)?.name || (id === self ? "this device" : shortID(id));
  const evidence = edge?.evidence ?? "unknown";
  const evidenceTone = evidence === "direct" ? "success" : evidence === "observed" ? "info" : evidence === "route" ? "neutral" : "warning";

  return (
    <InspectorFrame
      open
      title={`${nameOf(link.from)} → ${nameOf(link.to)}`}
      description="Link inspector"
      onClose={onClose}
    >
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="mono-label">direction</div>
          <div className="truncate font-mono text-xs">{shortID(link.from)} → {shortID(link.to)}</div>
        </div>
        <Badge tone={evidenceTone}>{evidence}</Badge>
      </div>

      <div className="grid grid-cols-2 gap-3 text-sm">
        <div><div className="mono-label">transport</div><div>{latest?.transport || "unknown"}</div></div>
        <div><div className="mono-label">reporting origin</div><div className="font-mono text-xs">{latest ? shortID(latest.origin) : "—"}</div></div>
        <div><div className="mono-label">rtt</div><div className="tabular">{latest ? `${latest.rtt_ms.toFixed(1)} ms` : "—"}</div></div>
        <div><div className="mono-label">jitter</div><div className="tabular">{latest ? `${latest.jitter_ms.toFixed(1)} ms` : "—"}</div></div>
      </div>

      <div>
        <div className="mono-label">throughput</div>
        {window ? (
          <div className="mt-1 space-y-1 text-xs">
            <div className="tabular flex flex-wrap items-center gap-3">
              <span className="inline-flex items-center gap-1"><ArrowDownRight size={13} />{formatBytesPerSec(window.rxPerSec)}</span>
              <span className="inline-flex items-center gap-1"><ArrowUpRight size={13} />{formatBytesPerSec(window.txPerSec)}</span>
            </div>
            <div className="text-[var(--text-muted)]">measured over {window.dtMs} ms</div>
          </div>
        ) : latest ? (
          <div className="mt-1 space-y-1 text-xs">
            <div className="tabular flex flex-wrap items-center gap-3">
              <span className="inline-flex items-center gap-1"><ArrowDownRight size={13} />{formatBytes(latest.rx_bytes)}</span>
              <span className="inline-flex items-center gap-1"><ArrowUpRight size={13} />{formatBytes(latest.tx_bytes)}</span>
            </div>
            <div className="text-[var(--text-muted)]">window unknown</div>
          </div>
        ) : (
          <div className="mt-1 text-xs text-[var(--text-muted)]">No fresh activity sample.</div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3 text-sm">
        <div>
          <div className="mono-label">evidence age</div>
          <div>
            {latest
              ? formatAge(Math.max(0, Date.now() - latest.observed_ms))
              : evidence === "direct"
                ? "current snapshot"
                : "—"}
          </div>
        </div>
        <div>
          <div className="mono-label">owning route</div>
          <div className="font-mono text-xs">
            {owning ? `${shortID(owning.destination)} · ${routeSummary(owning, nameOf)} · ${owning.hops} hops · ${owning.costMs.toFixed(1)} ms` : evidence === "direct" ? "direct link" : "—"}
          </div>
        </div>
      </div>
    </InspectorFrame>
  );
}
