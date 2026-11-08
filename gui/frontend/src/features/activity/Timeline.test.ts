import { describe, expect, it } from "vitest";
import { gapSpans, storedSeries, type StoredPoint } from "./Timeline";

const Interval = 5_000;

const point = (t: number, rx = 100, tx = 50): StoredPoint => ({ t, rx, tx });

describe("stored history chart", () => {
  it("keeps consecutive samples unbroken", () => {
    const points = [point(0), point(Interval), point(2 * Interval)];
    expect(storedSeries(points, Interval, "rx")).toEqual([100, 100, 100]);
    expect(gapSpans(points, Interval)).toEqual({ count: 0, ms: 0 });
  });

  it("breaks the line at a collection hole instead of drawing through it", () => {
    const points = [point(0), point(Interval), point(6 * Interval)];
    expect(storedSeries(points, Interval, "tx")).toEqual([50, 50, null, 50]);
    expect(gapSpans(points, Interval)).toEqual({ count: 1, ms: 4 * Interval });
  });

  it("treats a single missed interval as a gap, not a flat line", () => {
    const points = [point(0), point(3 * Interval)];
    expect(gapSpans(points, Interval).count).toBe(1);
    expect(storedSeries(points, Interval, "rx")).toEqual([100, null, 100]);
  });
});
