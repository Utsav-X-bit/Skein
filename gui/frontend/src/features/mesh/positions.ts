/**
 * Desktop peers use separate direct/relay rings. Mobile peers share one
 * ellipse: concentric rings in a narrow viewport would overlap their cards.
 * Node IDs determine the order, so telemetry updates never move a node.
 */
export type LayoutEntry = { id: string; direct: boolean; hops: number | null };

export type LayoutGeometry = {
  cx: number;
  cy: number;
  /** Horizontal squash of the ellipse (desktop 1.3: a circle would push cards past the short canvas). */
  ex: number;
  /** Vertical squash of the ellipse (desktop 0.75). */
  ey: number;
  innerRadius: number;
  outerRadius: number;
};

export const desktopGeometry: LayoutGeometry = {
  cx: 360,
  cy: 250,
  ex: 1.3,
  ey: 0.75,
  innerRadius: 260,
  outerRadius: 460,
};

/** Compact portrait geometry: 360×320 viewBox with room for five devices. */
export const mobileGeometry = {
  cx: 180,
  cy: 160,
  nodeWidth: 108,
  nodeHeight: 54,
  selfWidth: 140,
};

export function mobilePositionsFor(ids: string[], self: string): Map<string, { x: number; y: number }> {
  const geo = mobileGeometry;
  const positions = new Map([[self, { x: geo.cx, y: geo.cy }]]);
  const peers = ids.filter((id) => id !== self).sort();
  const rx = geo.cx - geo.nodeWidth / 2 - 10;
  const ry = geo.cy - geo.nodeHeight / 2 - 8;
  const start = peers.length === 2 || peers.length === 4 ? -3 * Math.PI / 4 : -Math.PI / 2;
  const step = peers.length === 2 ? Math.PI / 2 : 2 * Math.PI / peers.length;
  peers.forEach((id, i) => {
    const angle = start + i * step;
    positions.set(id, {
      x: geo.cx + Math.cos(angle) * rx,
      y: geo.cy + Math.sin(angle) * ry,
    });
  });
  return positions;
}

export function positionsFor(
  layout: LayoutEntry[],
  self: string,
  geo: LayoutGeometry,
): Map<string, { x: number; y: number }> {
  const positions = new Map<string, { x: number; y: number }>();
  const center = { x: geo.cx, y: geo.cy };
  positions.set(self, center);
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
        // The fan is a true circle (desktop behaviour, unchanged);
        // only the wide ellipse below is squashed.
        positions.set(entry.id, {
          x: center.x + Math.cos(angle) * radius,
          y: center.y + Math.sin(angle) * radius,
        });
      });
      return;
    }
    const step = (Math.PI * 2) / entries.length;
    entries.forEach((entry, i) => {
      const angle = start + i * step;
      positions.set(entry.id, {
        x: center.x + Math.cos(angle) * radius * geo.ex,
        y: center.y + Math.sin(angle) * radius * geo.ey,
      });
    });
  };
  place(inner, geo.innerRadius, -Math.PI / 2);
  place(outer, geo.outerRadius, -Math.PI / 2 + (inner.length > 0 ? Math.PI / inner.length : 0));
  return positions;
}
