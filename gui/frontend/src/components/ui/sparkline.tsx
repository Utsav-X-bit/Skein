import { useId } from "react";

export function Sparkline({ values, width = 160, height = 36, color = "var(--accent)" }: { values: number[]; width?: number; height?: number; color?: string }) {
  const id = useId();
  const max = Math.max(1, ...values);
  const step = values.length > 1 ? width / (values.length - 1) : width;
  const points = values.map((v, i) => `${i * step},${height - (v / max) * (height - 4) - 2}`).join(" ");
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} role="img" aria-label="Throughput trend">
      <defs>
        <linearGradient id={`g-${id}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.35" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <polyline points={points} fill="none" stroke={color} strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
      <polygon points={`0,${height} ${points} ${width},${height}`} fill={`url(#g-${id})`} />
    </svg>
  );
}
