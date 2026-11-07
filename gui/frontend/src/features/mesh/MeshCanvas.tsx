import { useEffect, useMemo, useState } from "react";
import { Background, Controls, MarkerType, ReactFlow, type Node, type NodeProps, Position } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { Activity, Peer } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { cn, shortID } from "@/lib/utils";
import { useUI } from "@/lib/store";
import { MeshEdge, type MeshFlowEdge } from "./MeshEdge";
import { MeshTable } from "./MeshTable";
import { useMotionAllowed } from "./hooks";
import {
  activityWindow,
  shouldFlow,
  type MeshModel,
  type MeshNode,
  type Rate,
} from "./model";

type Link = { from: string; to: string };

type NodeData = {
  node: MeshNode;
  selected: boolean;
  interactive: boolean;
  onSelect: (id: string) => void;
};

type LayoutEntry = { id: string; direct: boolean; hops: number | null };

const Center = { x: 360, y: 250 };
const InnerRadius = 150;
const OuterRadius = 270;

// Positions depend only on the sorted node-id list plus direct/hop facts, so
// a new model object every 2 s never moves a node.
function positionsFor(layout: LayoutEntry[], self: string): Map<string, { x: number; y: number }> {
  const positions = new Map<string, { x: number; y: number }>();
  positions.set(self, Center);
  const inner = layout.filter((e) => e.id !== self && e.direct).sort((a, b) => a.id.localeCompare(b.id));
  const outer = layout
    .filter((e) => e.id !== self && !e.direct)
    .sort((a, b) => (a.hops ?? Number.MAX_SAFE_INTEGER) - (b.hops ?? Number.MAX_SAFE_INTEGER) || a.id.localeCompare(b.id));
  const place = (entries: LayoutEntry[], radius: number, start: number) => {
    const step = (Math.PI * 2) / Math.max(1, entries.length);
    entries.forEach((entry, i) => {
      positions.set(entry.id, {
        x: Center.x + Math.cos(start + i * step) * radius,
        y: Center.y + Math.sin(start + i * step) * radius,
      });
    });
  };
  place(inner, InnerRadius, -Math.PI / 2);
  place(outer, OuterRadius, -Math.PI / 2 + (inner.length > 0 ? Math.PI / inner.length : 0));
  return positions;
}

// A rate exists only for consecutive seq numbers on one link with a proven
// window; everything else stays unknown and stops motion.
function linkRates(activities: Activity[]): Map<string, Rate> {
  const byPair = new Map<string, Activity[]>();
  for (const row of activities) {
    const key = `${row.origin}>${row.destination}`;
    const rows = byPair.get(key);
    if (rows) rows.push(row);
    else byPair.set(key, [row]);
  }
  const rates = new Map<string, Rate>();
  for (const [key, rows] of byPair) {
    rows.sort((a, b) => a.observed_ms - b.observed_ms || a.seq - b.seq);
    const last = rows[rows.length - 1];
    const prev = rows.find((r) => r.seq === last.seq - 1) ?? null;
    const window = activityWindow(prev, last);
    if (window && window.rxPerSec + window.txPerSec > 0) {
      rates.set(key, { rxPerSec: window.rxPerSec, txPerSec: window.txPerSec });
    }
  }
  return rates;
}

// React Flow's node subtree resolves inherited theme variables to the light
// palette in WebKit (the card rendered light on a dark shell). The node uses
// explicit theme values from the store so its surface cannot drift.
function MeshNodeView({ data }: NodeProps<Node<NodeData>>) {
  const { node, selected, interactive, onSelect } = data;
  const light = useUI((s) => s.theme) === "light";
  const palette = light
    ? { card: "#fbfaf6", text: "#14171c", border: "#7c7466", muted: "#4b5563", faint: "#66707e", accent: "#0b6b5a", up: "#17663a", relay: "#8a4e0a" }
    : { card: "#161b22", text: "#f4f6f8", border: "#66788f", muted: "#a7b0be", faint: "#8a94a3", accent: "#4fe3c1", up: "#7be3a0", relay: "#f2b35d" };
  return (
    <div
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : -1}
      aria-pressed={interactive ? selected : undefined}
      aria-label={`${node.label}${node.kind === "self" ? ", this device" : ""}, ${node.kind === "self" ? "up" : node.state}${node.rttMs !== undefined ? `, ${Math.round(node.rttMs)} milliseconds` : ""}`}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect(node.id);
        }
      }}
      onClick={() => onSelect(node.id)}
      style={{
        background: palette.card,
        color: palette.text,
        borderColor: selected ? palette.accent : palette.border,
        boxShadow: selected ? `0 0 0 1px ${palette.accent}` : undefined,
      }}
      className={cn(
        "w-[168px] cursor-pointer rounded-lg border px-3 py-2 text-left shadow-lg outline-none",
        "focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-xs font-medium">{node.label}</span>
        <span
          aria-hidden
          className="h-2 w-2 shrink-0 rounded-full"
          style={{
            background:
              node.state === "up" || node.kind === "self"
                ? palette.up
                : node.state === "degraded"
                  ? palette.relay
                  : palette.faint,
          }}
        />
      </div>
      <div className="mono-label mt-1 truncate" style={{ color: palette.faint }}>{node.overlay ?? shortID(node.id)}</div>
      <div className="mt-1 flex items-center justify-between gap-2 text-[10px]" style={{ color: palette.muted }}>
        <span className="truncate">{node.kind === "self" ? "up · this device" : `${node.state} · ${node.transport ?? "unknown"}`}</span>
        <span className="tabular shrink-0">{node.rttMs !== undefined ? `${Math.round(node.rttMs)} ms` : "—"}</span>
      </div>
    </div>
  );
}

const nodeTypes = { mesh: MeshNodeView };
const edgeTypes = { mesh: MeshEdge };

/** MeshCanvas renders the shared model as a map, with the table equivalent. */
export function MeshCanvas({
  model,
  peers,
  peerRates,
  activities,
  self,
  selectedPeer,
  selectedLink,
  onSelectPeer,
  onSelectLink,
  onEscape,
}: {
  model: MeshModel;
  peers: Peer[];
  peerRates: Record<string, Rate>;
  activities: Activity[];
  self: string;
  selectedPeer: string | null;
  selectedLink: Link | null;
  onSelectPeer: (id: string) => void;
  onSelectLink: (link: Link) => void;
  onEscape: () => void;
}) {
  const [mode, setMode] = useState<"map" | "table">("map");
  const motion = useMotionAllowed();

  const layout = useMemo(() => {
    const direct = new Set(model.edges.filter((e) => e.from === self && e.evidence === "direct").map((e) => e.to));
    const hops = new Map(model.paths.map((p) => [p.destination, p.hops]));
    return model.nodes
      .map((n) => ({ id: n.id, direct: direct.has(n.id), hops: hops.get(n.id) ?? null }))
      .sort((a, b) => a.id.localeCompare(b.id));
  }, [model.nodes, model.edges, model.paths, self]);

  const layoutKey = JSON.stringify(layout);
  const positions = useMemo(() => positionsFor(JSON.parse(layoutKey) as LayoutEntry[], self), [layoutKey, self]);
  const rates = useMemo(() => linkRates(activities), [activities]);

  const nodes = useMemo(
    () =>
      model.nodes.map((node) => ({
        id: node.id,
        type: "mesh",
        position: positions.get(node.id) ?? Center,
        data: { node, selected: selectedPeer === node.id, interactive: mode === "map", onSelect: onSelectPeer },
        draggable: false,
        selectable: true,
        // React Flow's default node wrapper is white; the themed card inside
        // must be the only surface.
        style: { background: "transparent", border: "none", padding: 0, boxShadow: "none" },
        sourcePosition: Position.Right,
        targetPosition: Position.Left,
      })),
    [model.nodes, positions, selectedPeer, onSelectPeer, mode],
  );

  const edges = useMemo(
    () =>
      model.edges.map((edge) => {
        const owner = edge.from === self ? edge.to : edge.from;
        const rate = edge.evidence === "direct" ? peerRates[owner] : rates.get(`${edge.from}>${edge.to}`) ?? rates.get(`${edge.to}>${edge.from}`);
        const flowing = shouldFlow(edge.evidence, rate, motion);
        const selected = selectedLink?.from === edge.from && selectedLink?.to === edge.to;
        const color = edge.evidence === "direct" ? "var(--accent)" : edge.evidence === "observed" ? "var(--info)" : "var(--text-faint)";
        return {
          id: edge.id,
          source: edge.from,
          target: edge.to,
          type: "mesh",
          selected,
          data: { flowing, color },
          style: {
            stroke: color,
            strokeWidth: selected ? 2.6 : 1.5,
            strokeDasharray: edge.evidence === "route" ? "2 4" : edge.evidence === "observed" ? "6 4" : undefined,
            opacity: edge.evidence === "route" ? 0.55 : 1,
          },
          markerEnd: { type: MarkerType.ArrowClosed, color, width: 14, height: 14 },
        } satisfies MeshFlowEdge;
      }),
    [model.edges, self, peerRates, rates, motion, selectedLink],
  );

  useEffect(() => {
    if (mode !== "map") return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      const target = e.target as HTMLElement | null;
      if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable)) return;
      onEscape();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mode, onEscape]);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div role="group" aria-label="Mesh view" className="inline-flex items-center gap-1 rounded-md border border-[var(--border)] bg-[var(--surface)] p-1">
          <Button size="sm" variant={mode === "map" ? "secondary" : "ghost"} aria-pressed={mode === "map"} onClick={() => setMode("map")}>
            Map
          </Button>
          <Button size="sm" variant={mode === "table" ? "secondary" : "ghost"} aria-pressed={mode === "table"} onClick={() => setMode("table")}>
            Table
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-4 text-[11px] text-[var(--text-muted)]">
          <span className="inline-flex items-center gap-1"><span className="inline-block h-0.5 w-5 bg-[var(--accent)]" /> direct</span>
          <span className="inline-flex items-center gap-1"><span className="inline-block h-0.5 w-5 border-t-2 border-dashed border-[var(--info)]" /> observed</span>
          <span className="inline-flex items-center gap-1"><span className="inline-block h-0.5 w-5 border-t-2 border-dotted border-[var(--text-faint)]" /> route</span>
          {!motion && <span>motion reduced · arrows only</span>}
        </div>
      </div>

      <div className="relative h-[clamp(360px,55vh,560px)] w-full overflow-hidden rounded-lg border border-[var(--border)] bg-[var(--surface)]">
        <div
          // display:none, not visibility:hidden: React Flow sets an inline
          // visibility on in-viewport nodes, which would override an
          // inherited hidden state and leave the map visible under the table.
          className={cn("h-full w-full", mode === "table" && "hidden")}
          aria-hidden={mode === "table"}
          inert={mode === "table" ? true : undefined}
        >
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            nodesDraggable={false}
            nodesConnectable={false}
            nodesFocusable={mode === "map"}
            edgesFocusable={mode === "map"}
            elementsSelectable
            fitView
            fitViewOptions={{ padding: 0.2, maxZoom: 1.2 }}
            minZoom={0.4}
            maxZoom={1.6}
            onNodeClick={(_, node) => onSelectPeer(node.id)}
            onEdgeClick={(_, edge) => onSelectLink({ from: edge.source, to: edge.target })}
            onPaneClick={onEscape}
            proOptions={{ hideAttribution: true }}
          >
            <Background color="var(--border)" gap={32} size={1} />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
        {mode === "table" && (
          <div className="absolute inset-0 overflow-auto p-2">
            <MeshTable
              model={model}
              peers={peers}
              peerRates={peerRates}
              selectedPeer={selectedPeer}
              selectedLink={selectedLink}
              onSelectPeer={onSelectPeer}
              onSelectLink={onSelectLink}
              onEscape={onEscape}
              self={self}
            />
          </div>
        )}
      </div>
    </div>
  );
}
