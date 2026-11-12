import { Fragment, useEffect, useMemo, useState } from "react";
import { Background, Controls, Handle, MarkerType, ReactFlow, type Node, type NodeProps, Position } from "@xyflow/react";
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
  facingSides,
  routeSummary,
  shouldFlow,
  type MeshModel,
  type MeshNode,
  type Rate,
} from "./model";

type Link = { from: string; to: string };

type NodeData = {
  node: MeshNode;
  routeText?: string;
  selected: boolean;
  interactive: boolean;
  onSelect: (id: string) => void;
};

type LayoutEntry = { id: string; direct: boolean; hops: number | null };

const Center = { x: 360, y: 250 };
const InnerRadius = 260;
const OuterRadius = 380;
const HandlePositions = [Position.Top, Position.Right, Position.Bottom, Position.Left];

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
    if (entries.length === 0) return;
    // Small meshes read best as a fan above the hub: two peers sit diagonally
    // left and right, so their mutual edge is a visible chord instead of a
    // line hidden behind the centre card. Four or five peers use a wide
    // ellipse: a circle would push the cards past the short canvas and force
    // the view to shrink the text.
    if (entries.length <= 3) {
      const spread = Math.PI / 2;
      const step = entries.length > 1 ? spread / (entries.length - 1) : 0;
      const first = -Math.PI / 2 - spread / 2;
      entries.forEach((entry, i) => {
        const angle = entries.length === 1 ? -Math.PI / 2 : first + i * step;
        positions.set(entry.id, {
          x: Center.x + Math.cos(angle) * radius,
          y: Center.y + Math.sin(angle) * radius,
        });
      });
      return;
    }
    const step = (Math.PI * 2) / entries.length;
    entries.forEach((entry, i) => {
      const angle = start + i * step;
      positions.set(entry.id, {
        x: Center.x + Math.cos(angle) * radius * 1.3,
        y: Center.y + Math.sin(angle) * radius * 0.75,
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
  const { node, routeText, selected, interactive, onSelect } = data;
  const light = useUI((s) => s.theme) === "light";
  const palette = light
    ? { card: "#fbfaf6", text: "#14171c", border: "#7c7466", muted: "#4b5563", faint: "#66707e", accent: "#0b6b5a", up: "#17663a", relay: "#8a4e0a" }
    : { card: "#161b22", text: "#f4f6f8", border: "#66788f", muted: "#a7b0be", faint: "#8a94a3", accent: "#4fe3c1", up: "#7be3a0", relay: "#f2b35d" };
  return (
    <div
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : -1}
      aria-pressed={interactive ? selected : undefined}
      aria-label={`${node.label}${node.kind === "self" ? ", this device" : ""}, ${node.kind === "self" ? "up" : node.state}${routeText ? `, ${routeText}` : ""}${node.rttMs !== undefined ? `, ${Math.round(node.rttMs)} milliseconds` : ""}`}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect(node.id);
        }
      }}
      onClick={() => onSelect(node.id)}
      style={{
        background: `linear-gradient(180deg, ${palette.card}, color-mix(in oklch, ${palette.card} 86%, #000))`,
        color: palette.text,
        borderColor:
          selected
            ? palette.accent
            : node.kind === "self"
              ? `color-mix(in oklch, ${palette.accent} 45%, ${palette.border})`
              : palette.border,
        boxShadow:
          node.kind === "self"
            ? `0 0 0 1px ${palette.accent}22, 0 14px 32px -18px ${palette.accent}66`
            : selected
              ? `0 0 0 1px ${palette.accent}`
              : undefined,
      }}
      className={cn(
        "w-[178px] cursor-pointer rounded-[14px] border px-3 pb-2.5 pt-2.5 text-left shadow-lg outline-none transition-colors",
        "hover:border-[var(--border-strong)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
      )}
    >
      {HandlePositions.map((position) => (
        <Fragment key={position}>
          <Handle id={`source-${position}`} type="source" position={position} isConnectable={false} style={{ visibility: "hidden" }} />
          <Handle id={`target-${position}`} type="target" position={position} isConnectable={false} style={{ visibility: "hidden" }} />
        </Fragment>
      ))}
      <div className="flex items-center gap-2">
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
            boxShadow: node.state === "up" || node.kind === "self" ? `0 0 8px ${palette.up}88` : undefined,
          }}
        />
        <span className="min-w-0 flex-1 truncate text-[13px] font-medium tracking-tight">{node.label}</span>
        {node.kind === "self" && (
          <span
            className="shrink-0 rounded-full border px-1.5 py-0.5 text-[8px] tracking-[0.12em]"
            style={{
              borderColor: `color-mix(in oklch, ${palette.accent} 35%, transparent)`,
              color: palette.accent,
              background: `color-mix(in oklch, ${palette.accent} 8%, transparent)`,
            }}
          >
            HUB
          </span>
        )}
      </div>
      <div className="mt-1 truncate font-mono text-[10px]" style={{ color: palette.faint }}>{node.overlay ?? shortID(node.id)}</div>
      <div className="mt-1.5 flex items-center justify-between gap-2 text-[10px]" style={{ color: palette.muted }}>
        <span className="truncate">{node.kind === "self" ? "up · this device" : `${node.state} · ${node.transport ?? "unknown"}`}</span>
        <span className="tabular shrink-0">{node.rttMs !== undefined ? `${Math.round(node.rttMs)} ms` : "—"}</span>
      </div>
      {routeText && (
        <div className="mt-0.5 truncate text-[10px]" style={{ color: palette.faint }}>{routeText}</div>
      )}
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
  const visibleEdges = useMemo(() => model.edges, [model.edges]);
  const evidencePresent = useMemo(() => new Set(visibleEdges.map((edge) => edge.evidence)), [visibleEdges]);

  const nameOf = useMemo(
    () => (id: string) => (id === self ? "This device" : peers.find((p) => p.id === id)?.name || shortID(id)),
    [peers, self],
  );

  const nodes = useMemo(() => {
    return model.nodes.map((node) => {
      const route = model.paths.find((p) => p.destination === node.id);
      const routeText =
        node.kind === "self"
          ? undefined
          : route
            ? routeSummary(route, nameOf)
            : node.kind === "peer"
              ? "no route"
              : undefined;
      return {
        id: node.id,
        type: "mesh",
        position: positions.get(node.id) ?? Center,
        data: { node, routeText, selected: selectedPeer === node.id, interactive: mode === "map", onSelect: onSelectPeer },
        draggable: false,
        selectable: true,
        // React Flow's default node wrapper is white; the themed card inside
        // must be the only surface.
        style: { background: "transparent", border: "none", padding: 0, boxShadow: "none" },
      };
    });
  }, [model.nodes, model.paths, peers, positions, selectedPeer, onSelectPeer, mode, nameOf]);

  const edges = useMemo(
    () =>
      visibleEdges.map((edge) => {
        const owner = edge.from === self ? edge.to : edge.from;
        const rate = edge.evidence === "direct" ? peerRates[owner] : rates.get(`${edge.from}>${edge.to}`) ?? rates.get(`${edge.to}>${edge.from}`);
        const flowing = shouldFlow(edge.evidence, rate, motion);
        const selected = selectedLink?.from === edge.from && selectedLink?.to === edge.to;
        const label =
          edge.evidence === "advertised" && edge.hops && edge.hops > 1
            ? `via ${edge.path?.[1] === self ? "you" : nameOf(edge.path?.[1] ?? edge.to)}`
            : undefined;
        const color =
          edge.evidence === "direct"
            ? "var(--accent)"
            : edge.evidence === "observed"
              ? "var(--info)"
              : edge.evidence === "advertised"
                ? "var(--warning)"
                : "var(--text-faint)";
        const from = positions.get(edge.from) ?? Center;
        const to = positions.get(edge.to) ?? Center;
        const [sourceSide, targetSide] = facingSides(to.x - from.x, to.y - from.y);
        return {
          id: edge.id,
          source: edge.from,
          target: edge.to,
          sourceHandle: `source-${sourceSide}`,
          targetHandle: `target-${targetSide}`,
          type: "mesh",
          selected,
          data: { flowing, color, label },
          style: {
            stroke: color,
            strokeWidth: selected ? 2.8 : edge.evidence === "advertised" ? 2.2 : edge.evidence === "direct" ? 2 : 1.6,
            strokeDasharray:
              edge.evidence === "route" ? "2 4" : edge.evidence === "observed" ? "6 4" : edge.evidence === "advertised" ? "4 4" : undefined,
            opacity: edge.evidence === "route" ? 0.5 : 1,
          },
          markerEnd:
            edge.evidence === "advertised"
              ? undefined
              : { type: MarkerType.ArrowClosed, color, width: 14, height: 14 },
        } satisfies MeshFlowEdge;
        }),
    [visibleEdges, positions, self, peerRates, rates, motion, selectedLink, nameOf],
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

      <div className="mesh-canvas relative h-[clamp(360px,55vh,560px)] w-full overflow-hidden rounded-lg border border-[var(--border)]">
        <div
          // display:none, not visibility:hidden: React Flow sets an inline
          // visibility on in-viewport nodes, which would override an
          // inherited hidden state and leave the map visible under the table.
          className={cn("h-full w-full", mode === "table" && "hidden")}
          aria-hidden={mode === "table"}
          inert={mode === "table" ? true : undefined}
        >
          <ReactFlow
            key={layoutKey}
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
            fitViewOptions={{ padding: 0.2, maxZoom: 1.3 }}
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
