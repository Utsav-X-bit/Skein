import { useMemo, useState } from "react";
import { Pause, Play } from "lucide-react";
import type { Peer } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { displayPeerNameById } from "@/lib/utils";
import { useUI } from "@/lib/store";
import { useMotionAllowed } from "@/features/mesh/hooks";
import { mobileGeometry, positionsFor, type LayoutEntry } from "@/features/mesh/positions";
import type { MeshModel, Rate } from "@/features/mesh/model";
import { MobileMeshList } from "@/features/mesh/MobileMeshList";

type Link = { from: string; to: string };

const NODE_W = 100;
const NODE_H = 54;
const SELF_W = 120;
const SELF_H = 56;

function edgeColor(evidence: string): string {
  switch (evidence) {
    case "direct":
      return "var(--accent)";
    case "observed":
      return "var(--info)";
    case "advertised":
      return "var(--warning)";
    default:
      return "var(--text-faint)";
  }
}

function edgeDash(evidence: string): string | undefined {
  switch (evidence) {
    case "route":
      return "2 4";
    case "observed":
      return "6 4";
    case "advertised":
      return "4 4";
    default:
      return undefined;
  }
}

/**
 * MobileMeshMap is the compact home map (design brief §3): this node
 * centred, peers on the ellipse from the shared positionsFor layout,
 * straight links with RTT at the midpoint, tap peer → detail sheet,
 * tap link → link detail. Flow is deliberately quieter than desktop:
 * one travelling dot per active link, max 3, marching dashes only on
 * the selected link — and nothing at all when motion is reduced or
 * the Settings → Motion "Animate mesh flow" switch is off.
 */
export function MobileMeshMap({
  model,
  peers,
  peerRates,
  self,
  selectedPeer,
  selectedLink,
  onSelectPeer,
  onSelectLink,
  onEscape,
  stale,
  staleText,
  onRetry,
}: {
  model: MeshModel;
  peers: Peer[];
  peerRates: Record<string, Rate>;
  self: string;
  selectedPeer: string | null;
  selectedLink: Link | null;
  onSelectPeer: (id: string) => void;
  onSelectLink: (link: Link) => void;
  onEscape: () => void;
  stale: boolean;
  staleText?: string;
  onRetry: () => void;
}) {
  const [mode, setMode] = useState<"map" | "table">("map");
  const motion = useMotionAllowed();
  const animateFlow = useUI((s) => s.animateFlow);
  const setAnimateFlow = useUI((s) => s.setAnimateFlow);
  const flowOn = motion && animateFlow;

  const layout = useMemo(() => {
    const direct = new Set(model.edges.filter((e) => e.from === self && e.evidence === "direct").map((e) => e.to));
    const hops = new Map(model.paths.map((p) => [p.destination, p.hops]));
    return model.nodes
      .map((n) => ({ id: n.id, direct: direct.has(n.id), hops: hops.get(n.id) ?? null }) as LayoutEntry)
      .sort((a, b) => a.id.localeCompare(b.id));
  }, [model.nodes, model.edges, model.paths, self]);

  const layoutKey = JSON.stringify(layout);
  const positions = useMemo(
    () => positionsFor(JSON.parse(layoutKey) as LayoutEntry[], self, mobileGeometry),
    [layoutKey, self],
  );

  const nameOf = (id: string) => (id === self ? "This device" : displayPeerNameById(id, peers));
  const peerById = useMemo(() => new Map(peers.map((p) => [p.id, p])), [peers]);
  const evidencePresent = useMemo(() => new Set(model.edges.map((e) => e.evidence)), [model.edges]);

  // Active links for flow dots: a self-link with live traffic in the
  // last sampling window, capped at 3 dots on screen.
  const activeLinks = useMemo(() => {
    if (!flowOn) return [];
    return model.edges
      .filter((e) => e.from === self || e.to === self)
      .map((e) => {
        const other = e.from === self ? e.to : e.from;
        const rate = peerRates[other];
        const active = rate ? rate.rxPerSec + rate.txPerSec > 0 : false;
        return { edge: e, other, rate, active };
      })
      .filter((x) => x.active)
      .sort((a, b) => (b.rate!.rxPerSec + b.rate!.txPerSec) - (a.rate!.rxPerSec + a.rate!.txPerSec))
      .slice(0, 3);
  }, [model.edges, peerRates, self, flowOn]);

  const summary = useMemo(() => {
    const ups = model.nodes.filter((n) => n.kind !== "self").length;
    const parts = model.nodes
      .filter((n) => n.kind !== "self")
      .map((n) => `${nameOf(n.id)}, ${n.state}${n.rttMs !== undefined ? `, ${Math.round(n.rttMs)} milliseconds` : ""}`);
    return `Mesh map, ${ups} peer${ups === 1 ? "" : "s"}. ${parts.join(". ")}`;
  }, [model.nodes, peers, self]);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div role="group" aria-label="Mesh view" className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--surface)] p-1">
          <Button size="sm" variant={mode === "map" ? "secondary" : "ghost"} aria-pressed={mode === "map"} onClick={() => setMode("map")}>
            Map
          </Button>
          <Button size="sm" variant={mode === "table" ? "secondary" : "ghost"} aria-pressed={mode === "table"} onClick={() => setMode("table")}>
            List
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-[var(--border)] bg-[var(--surface)]/80 px-3 py-1.5 text-[11px] text-[var(--text-muted)]">
          {[
            { key: "direct", label: "direct", sample: <span className="inline-block h-0.5 w-5 bg-[var(--accent)]" /> },
            { key: "observed", label: "observed", sample: <span className="inline-block h-0.5 w-5 border-t-2 border-dashed border-[var(--info)]" /> },
            { key: "advertised", label: "advertised", sample: <span className="inline-block h-0.5 w-5 border-t-2 border-dashed border-[var(--warning)]" /> },
            { key: "route", label: "route", sample: <span className="inline-block h-0.5 w-5 border-t-2 border-dotted border-[var(--text-faint)]" /> },
          ]
            .filter((item) => evidencePresent.has(item.key as "direct" | "observed" | "advertised" | "route"))
            .map((item) => (
              <span key={item.key} className="inline-flex items-center gap-1">
                {item.sample}
                {item.label}
              </span>
            ))}
          {!motion && <span>motion reduced · arrows only</span>}
        </div>
      </div>

      <div className="mesh-canvas relative w-full overflow-hidden rounded-lg border border-[var(--border)]">
        {mode === "table" ? (
          <div className="max-h-[480px] overflow-auto">
            <MobileMeshList
              model={model}
              peers={peers}
              peerRates={peerRates}
              self={self}
              selectedPeer={selectedPeer}
              selectedLink={selectedLink}
              onSelectPeer={onSelectPeer}
              onSelectLink={onSelectLink}
            />
          </div>
        ) : (
          <div role="img" aria-label={summary} className="relative">
            <button
              type="button"
              aria-label={flowOn ? "Pause mesh animation" : "Play mesh animation"}
              aria-pressed={flowOn}
              onClick={() => setAnimateFlow(!flowOn)}
              className="absolute bottom-2 right-2 z-10 flex h-12 w-12 items-center justify-center rounded-full border border-[var(--border-strong)] bg-[var(--surface)] text-[var(--text-muted)]"
            >
              {flowOn ? <Pause size={18} /> : <Play size={18} />}
            </button>
            {stale && (
              <div className="absolute inset-x-3 top-2 z-10 flex flex-wrap items-center gap-3 rounded-md border border-[var(--warning)]/40 bg-[var(--surface)] px-3 py-2 text-xs text-[var(--warning)]">
                <span className="min-w-0 flex-1">{staleText ?? "Showing the last known state."}</span>
                <Button size="sm" variant="secondary" onClick={onRetry}>
                  Retry
                </Button>
              </div>
            )}
            <svg
              viewBox="0 0 360 320"
              className="h-auto w-full"
              onClick={(e) => {
                if (e.target === e.currentTarget) onEscape();
              }}
            >
              {/* Links */}
              {model.edges.map((edge) => {
                const from = positions.get(edge.from);
                const to = positions.get(edge.to);
                if (!from || !to) return null;
                const isSelfLink = edge.from === self || edge.to === self;
                const isSelected =
                  selectedLink !== null &&
                  ((selectedLink.from === edge.from && selectedLink.to === edge.to) ||
                    (selectedLink.from === edge.to && selectedLink.to === edge.from));
                const color = edgeColor(edge.evidence);
                const midX = (from.x + to.x) / 2;
                const midY = (from.y + to.y) / 2;
                const rtt = edge.rttMs ?? (isSelfLink ? peerById.get(edge.from === self ? edge.to : edge.from)?.rtt_ms : undefined);
                return (
                  <g key={edge.id}>
                    <line
                      x1={from.x}
                      y1={from.y}
                      x2={to.x}
                      y2={to.y}
                      stroke={color}
                      strokeWidth={isSelected ? 2.8 : isSelfLink ? 2 : 1}
                      strokeDasharray={isSelected && flowOn ? undefined : edgeDash(edge.evidence)}
                      opacity={edge.evidence === "route" ? 0.5 : 1}
                      className={isSelected && flowOn ? "animate-flow" : undefined}
                      style={isSelected && flowOn ? { strokeDasharray: "6 6" } : undefined}
                    />
                    {/* 24dp-wide invisible hit strip along the line */}
                    <line
                      x1={from.x}
                      y1={from.y}
                      x2={to.x}
                      y2={to.y}
                      stroke="transparent"
                      strokeWidth={24}
                      onClick={(e) => {
                        e.stopPropagation();
                        onSelectLink({ from: edge.from, to: edge.to });
                      }}
                      style={{ cursor: "pointer" }}
                    >
                      <title>{`${nameOf(edge.from)} to ${nameOf(edge.to)}, ${edge.evidence}`}</title>
                    </line>
                    {isSelfLink && rtt !== undefined && (
                      <g>
                        <rect x={midX - 22} y={midY - 9} width={44} height={18} rx={9} fill="var(--surface)" stroke="var(--border)" />
                        <text x={midX} y={midY + 4} textAnchor="middle" fontSize={10} fill="var(--text-muted)" className="tabular">
                          {Math.round(rtt)} ms
                        </text>
                      </g>
                    )}
                  </g>
                );
              })}
              {/* Flow dots: one per active link, max 3 */}
              {activeLinks.map(({ edge }) => {
                const from = positions.get(edge.from)!;
                const to = positions.get(edge.to)!;
                const path = `M ${from.x} ${from.y} L ${to.x} ${to.y}`;
                return (
                  <circle key={`flow-${edge.id}`} r={3.5} fill="var(--accent)" style={{ willChange: "transform" }}>
                    <animateMotion dur="1.4s" repeatCount="indefinite" path={path} />
                  </circle>
                );
              })}
              {/* Nodes */}
              {model.nodes.map((node) => {
                const pos = positions.get(node.id) ?? { x: mobileGeometry.cx, y: mobileGeometry.cy };
                const isSelf = node.kind === "self";
                const w = isSelf ? SELF_W : NODE_W;
                const h = isSelf ? SELF_H : NODE_H;
                const selected = selectedPeer === node.id;
                const peer = isSelf ? undefined : peerById.get(node.id);
                const direct = model.paths.find((p) => p.destination === node.id)?.direct;
                const label = nameOf(node.id);
                return (
                  <g
                    key={node.id}
                    role="button"
                    tabIndex={0}
                    aria-label={`${label}${isSelf ? ", this device" : ""}, ${node.state}${node.rttMs !== undefined ? `, ${Math.round(node.rttMs)} milliseconds` : ""}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      onSelectPeer(node.id);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        onSelectPeer(node.id);
                      }
                    }}
                    style={{ cursor: "pointer" }}
                  >
                    <rect
                      x={pos.x - w / 2}
                      y={pos.y - h / 2}
                      width={w}
                      height={h}
                      rx={12}
                      fill="var(--surface-raised)"
                      stroke={selected ? "var(--accent)" : "var(--border-strong)"}
                      strokeWidth={selected ? 2 : 1}
                    />
                    <circle
                      cx={pos.x - w / 2 + 14}
                      cy={pos.y - h / 2 + 14}
                      r={4}
                      fill={node.state === "up" || isSelf ? "var(--success)" : node.state === "degraded" ? "var(--relay)" : "var(--text-faint)"}
                    />
                    <text x={pos.x - w / 2 + 24} y={pos.y - h / 2 + 18} fontSize={12} fontWeight={600} fill="var(--text)">
                      {label.length > 14 ? `${label.slice(0, 13)}…` : label}
                    </text>
                    <text x={pos.x - w / 2 + 10} y={pos.y - h / 2 + 34} fontSize={10} fill="var(--text-faint)" fontFamily="monospace">
                      {node.overlay ? (node.overlay.length > 13 ? `${node.overlay.slice(0, 12)}…` : node.overlay) : "no overlay yet"}
                    </text>
                    <text x={pos.x - w / 2 + 10} y={pos.y - h / 2 + 47} fontSize={10} fill="var(--text-muted)">
                      {isSelf ? "up · this device" : `${node.state} · ${direct === undefined ? peer?.transport ?? "unknown" : direct ? "direct" : "relayed"}`}
                    </text>
                    {!isSelf && node.rttMs !== undefined && (
                      <text x={pos.x + w / 2 - 10} y={pos.y - h / 2 + 47} textAnchor="end" fontSize={10} fill="var(--text-muted)" className="tabular">
                        {Math.round(node.rttMs)} ms
                      </text>
                    )}
                  </g>
                );
              })}
            </svg>
            {model.nodes.filter((n) => n.kind !== "self").length === 0 && (
              <p className="absolute inset-x-0 bottom-2 text-center text-xs text-[var(--text-muted)]">
                No peers yet — invite a device to join this mesh.
              </p>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
