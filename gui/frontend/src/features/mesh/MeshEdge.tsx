import { BaseEdge, EdgeLabelRenderer, getStraightPath, type Edge, type EdgeProps } from "@xyflow/react";

export type MeshEdgeData = {
  flowing: boolean;
  color: string;
  /** Short label rendered at the edge midpoint (advertised peer links). */
  label?: string;
};
export type MeshFlowEdge = Edge<MeshEdgeData, "mesh">;

/**
 * MeshEdge draws the static link plus a single flow marker. The marker is
 * moved by SMIL `animateMotion`, so the browser animates the transform along
 * the path without React state or stroke-dashoffset animation.
 */
export function MeshEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  markerEnd,
  interactionWidth,
  style,
  data,
}: EdgeProps<MeshFlowEdge>) {
  const [edgePath, labelX, labelY] = getStraightPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
  });
  return (
    <>
      <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} interactionWidth={interactionWidth} style={style} />
      {data?.flowing && (
        <circle r={2.5} fill={data.color} pointerEvents="none">
          <animateMotion path={edgePath} dur="2s" repeatCount="indefinite" />
        </circle>
      )}
      {data?.label && (
        <EdgeLabelRenderer>
          <div
            className="nodrag nopan"
            style={{
              position: "absolute",
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px) translateY(-11px)`,
              background: "color-mix(in oklch, var(--warning) 10%, var(--surface))",
              border: "1px solid color-mix(in oklch, var(--warning) 45%, transparent)",
              color: "var(--warning)",
              borderRadius: 999,
              padding: "3px 8px",
              fontFamily: "var(--font-mono)",
              fontSize: 9,
              letterSpacing: "0.08em",
              textTransform: "uppercase",
              whiteSpace: "nowrap",
              pointerEvents: "none",
            }}
          >
            {data.label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
