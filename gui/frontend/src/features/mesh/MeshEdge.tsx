import { BaseEdge, EdgeLabelRenderer, getBezierPath, type Edge, type EdgeProps } from "@xyflow/react";

export type MeshEdgeData = {
  flowing: boolean;
  color: string;
  /** Short label rendered at the edge midpoint (advertised peer links). */
  label?: string;
  curvature?: number;
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
  sourcePosition,
  targetX,
  targetY,
  targetPosition,
  markerEnd,
  interactionWidth,
  style,
  data,
}: EdgeProps<MeshFlowEdge>) {
  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
    curvature: data?.curvature ?? 0.2,
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
              background: "var(--surface)",
              border: "1px solid var(--border)",
              color: "var(--text-muted)",
              borderRadius: 999,
              padding: "2px 6px",
              fontFamily: "var(--font-mono)",
              fontSize: 8,
              letterSpacing: "0.06em",
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
