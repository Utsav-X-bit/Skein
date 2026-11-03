import { useMemo } from "react";
import { Background, Controls, MiniMap, ReactFlow, type Edge, type Node, Position } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { Peer, Status } from "@/lib/api";
import { shortID } from "@/lib/utils";

type PeerData = { label: string; state: string; direct: boolean; rtt?: number; local?: boolean };

function PeerNode({ data }: { data: PeerData }) {
  return (
    <div
      className={`min-w-[132px] rounded-lg border px-3 py-2 shadow-lg ${data.local ? "border-[var(--accent)]" : "border-[var(--border-strong)]"}`}
      style={{ background: "#101216", color: "#F4F6F8" }}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-xs font-medium">{data.label}</span>
        <span className={`h-2 w-2 rounded-full ${data.state === "up" ? "bg-[var(--success)]" : data.state === "degraded" ? "bg-[var(--relay)]" : "bg-[var(--text-faint)]"}`} />
      </div>
      <div className="mono-label mt-1">{data.local ? "this device" : data.direct ? "direct" : "relay"}{data.rtt ? ` · ${data.rtt.toFixed(1)}ms` : ""}</div>
    </div>
  );
}

export function Topology({ status, peers, onSelect }: { status: Status; peers: Peer[]; onSelect?: (id: string) => void }) {
  const { nodes, edges } = useMemo(() => {
    const center = { x: 360, y: 220 };
    const ns: Node[] = [{
      id: "me", position: center, data: { label: status.name || "this device", state: "up", direct: true, local: true },
      sourcePosition: Position.Right, targetPosition: Position.Left, type: "peer",
    }];
    const es: Edge[] = [];
    const radius = Math.max(170, peers.length * 34);
    peers.forEach((p, i) => {
      const angle = (i / Math.max(1, peers.length)) * Math.PI * 2 - Math.PI / 2;
      const pos = { x: center.x + Math.cos(angle) * radius, y: center.y + Math.sin(angle) * radius };
      ns.push({ id: p.id, position: pos, data: { label: p.name || shortID(p.id), state: p.state, direct: p.direct, rtt: p.rtt_ms }, type: "peer" });
      es.push({
        id: `e-${p.id}`, source: "me", target: p.id, animated: p.state === "up" && p.direct,
        style: { stroke: p.direct ? "var(--accent)" : "var(--relay)", strokeWidth: 1.5, strokeDasharray: p.direct ? undefined : "6 6" },
      });
    });
    return { nodes: ns, edges: es };
  }, [status, peers]);

  return (
    <div className="h-[420px] w-full overflow-hidden rounded-lg border border-[var(--border)] bg-[var(--surface)]">
      <ReactFlow nodes={nodes} edges={edges} nodeTypes={{ peer: PeerNode }} fitView onNodeClick={(_, n) => onSelect?.(n.id)} proOptions={{ hideAttribution: true }} minZoom={0.4} maxZoom={1.6}>
        <Background color="var(--border)" gap={32} size={1} />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable nodeColor="var(--accent)" maskColor="rgba(0,0,0,.55)" style={{ background: "#101216" }} />
      </ReactFlow>
    </div>
  );
}
