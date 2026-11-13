import { describe, expect, it } from "vitest";
import { desktopGeometry, mobileGeometry, mobilePositionsFor, positionsFor, type LayoutEntry } from "./positions";

const layout = (ids: string[]): LayoutEntry[] => ids.map((id) => ({ id, direct: true, hops: 1 }));

describe("positionsFor", () => {
  it.each([0, 1, 2, 3, 4])("keeps %i mobile peers and the local card separated and inside the map", (count) => {
    const ids = ["self", ...["a", "b", "c", "d"].slice(0, count)];
    const positions = mobilePositionsFor(ids, "self");
    expect(positions.get("self")).toEqual({ x: mobileGeometry.cx, y: mobileGeometry.cy });
    const cards = ids.map((id) => ({
      ...positions.get(id)!,
      w: id === "self" ? mobileGeometry.selfWidth : mobileGeometry.nodeWidth,
      h: mobileGeometry.nodeHeight,
    }));
    for (const card of cards) {
      expect(card.x - card.w / 2).toBeGreaterThanOrEqual(8);
      expect(card.x + card.w / 2).toBeLessThanOrEqual(352);
      expect(card.y - card.h / 2).toBeGreaterThanOrEqual(8);
      expect(card.y + card.h / 2).toBeLessThanOrEqual(312);
    }
    for (let i = 0; i < cards.length; i++) {
      for (let j = i + 1; j < cards.length; j++) {
        const a = cards[i], b = cards[j];
        expect(
          Math.abs(a.x - b.x) >= (a.w + b.w) / 2 + 8 ||
          Math.abs(a.y - b.y) >= (a.h + b.h) / 2 + 8,
        ).toBe(true);
      }
    }
  });

  it("keeps mobile positions stable when peer ordering changes", () => {
    expect(mobilePositionsFor(["self", "c", "a", "b"], "self")).toEqual(
      mobilePositionsFor(["b", "a", "self", "c"], "self"),
    );
  });

  it("preserves the desktop fan above the local node", () => {
    const pos = positionsFor(layout(["self", "a", "b"]), "self", desktopGeometry);
    expect(pos.get("self")).toEqual({ x: desktopGeometry.cx, y: desktopGeometry.cy });
    expect(pos.get("a")!.x).toBeLessThan(desktopGeometry.cx);
    expect(pos.get("b")!.x).toBeGreaterThan(desktopGeometry.cx);
    for (const id of ["a", "b"]) {
      const p = pos.get(id)!;
      expect(p.y).toBeLessThan(desktopGeometry.cy);
      expect(Math.hypot(p.x - desktopGeometry.cx, p.y - desktopGeometry.cy)).toBeCloseTo(desktopGeometry.innerRadius);
    }
  });
});
