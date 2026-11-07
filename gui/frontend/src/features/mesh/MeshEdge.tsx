import { BaseEdge, getBezierPath, type Edge, type EdgeProps } from "@xyflow/react";

export type MeshEdgeData = { flowing: boolean; color: string };
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
  sourcePosition,
  targetPosition,
  markerEnd,
  interactionWidth,
  style,
  data,
}: EdgeProps<MeshFlowEdge>) {
  const [edgePath] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });
  return (
    <>
      <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} interactionWidth={interactionWidth} style={style} />
      {data?.flowing && (
        <circle r={2.5} fill={data.color} pointerEvents="none">
          <animateMotion path={edgePath} dur="2s" repeatCount="indefinite" />
        </circle>
      )}
    </>
  );
}
