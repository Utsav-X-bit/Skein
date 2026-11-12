import { describe, expect, it } from "vitest";
import { desktopGeometry, mobileGeometry, positionsFor, type LayoutEntry } from "./positions";

const layout = (ids: string[]): LayoutEntry[] => ids.map((id) => ({ id, direct: true, hops: 1 }));

describe("positionsFor", () => {
  it("centres the self node", () => {
    const pos = positionsFor(layout(["self", "a"]), "self", mobileGeometry);
    expect(pos.get("self")).toEqual({ x: mobileGeometry.cx, y: mobileGeometry.cy });
  });

  it("places up to 3 peers on a true circle (fan case)", () => {
    const pos = positionsFor(layout(["self", "a", "b", "c"]), "self", mobileGeometry);
    const dist = (id: string) => {
      const p = pos.get(id)!;
      return Math.hypot(p.x - mobileGeometry.cx, p.y - mobileGeometry.cy);
    };
    // All three on the same radius (the fan is a circle, not an ellipse).
    expect(dist("a")).toBeCloseTo(dist("b"), 6);
    expect(dist("b")).toBeCloseTo(dist("c"), 6);
  });

  it("uses the ellipse for 4+ peers and keeps every node inside the viewBox", () => {
    const pos = positionsFor(layout(["self", "a", "b", "c", "d"]), "self", mobileGeometry);
    for (const [id, p] of pos) {
      expect(p.x).toBeGreaterThanOrEqual(0);
      expect(p.x).toBeLessThanOrEqual(360);
      expect(p.y).toBeGreaterThanOrEqual(0);
      expect(p.y).toBeLessThanOrEqual(320);
      expect(id).toBeTruthy();
    }
    // Ellipse radii differ from the fan radius.
    const fan = positionsFor(layout(["self", "a", "b", "c"]), "self", mobileGeometry).get("a")!;
    const wide = pos.get("a")!;
    expect(Math.hypot(wide.x - mobileGeometry.cx, wide.y - mobileGeometry.cy)).not.toBeCloseTo(
      Math.hypot(fan.x - mobileGeometry.cx, fan.y - mobileGeometry.cy),
      0,
    );
  });

  it("matches the desktop geometry for the desktop canvas", () => {
    const pos = positionsFor(layout(["self", "a"]), "self", desktopGeometry);
    expect(pos.get("self")).toEqual({ x: desktopGeometry.cx, y: desktopGeometry.cy });
    expect(pos.get("a")).toBeDefined();
  });
});
