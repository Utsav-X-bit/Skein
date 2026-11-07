import { describe, expect, it } from "vitest";
import type { Activity, Peer, Route } from "@/lib/api";
import {
  activityWindow,
  buildMesh,
  bucketActivities,
  computeRate,
  dedupeActivities,
  formatAge,
  formatBytesPerSec,
  formatRate,
  peerHealth,
  presenceOf,
} from "./model";

const peer = (id: string, over: Partial<Peer> = {}): Peer => ({
  id,
  state: "up",
  rtt_ms: 0,
  dialer: false,
  direct: true,
  rx_bytes: 0,
  tx_bytes: 0,
  last_seen_ms: 0,
  ...over,
});

const route = (destination: string, path: string[], over: Partial<Route> = {}): Route => ({
  destination,
  next_hop: path[0] ?? destination,
  cost_ms: 10,
  hops: path.length,
  path,
  ...over,
});

const activity = (over: Partial<Activity> = {}): Activity => ({
  origin: "self",
  destination: "p1",
  seq: 1,
  rx_bytes: 0,
  tx_bytes: 0,
  rtt_ms: 0,
  jitter_ms: 0,
  transport: "quic",
  direct: true,
  observed_ms: 1_000,
  expires_ms: 60_000,
  path: null,
  ...over,
});

describe("computeRate", () => {
  it("returns null for the first sample", () => {
    expect(computeRate(null, { t: 1_000, rx: 10, tx: 20, uptime: 1 })).toBeNull();
  });

  it("divides steady counter deltas by elapsed seconds", () => {
    const rate = computeRate(
      { t: 1_000, rx: 1_000, tx: 500, uptime: 10 },
      { t: 3_000, rx: 3_000, tx: 1_500, uptime: 12 },
    );
    expect(rate).toEqual({ rxPerSec: 1_000, txPerSec: 500 });
  });

  it("returns null when elapsed time is not positive", () => {
    expect(computeRate({ t: 1_000, rx: 1, tx: 1, uptime: 1 }, { t: 1_000, rx: 2, tx: 2, uptime: 1 })).toBeNull();
    expect(computeRate({ t: 2_000, rx: 1, tx: 1, uptime: 1 }, { t: 1_000, rx: 2, tx: 2, uptime: 1 })).toBeNull();
  });

  it("returns null when uptime goes backwards (daemon restart)", () => {
    expect(computeRate({ t: 1_000, rx: 1, tx: 1, uptime: 100 }, { t: 2_000, rx: 2, tx: 2, uptime: 5 })).toBeNull();
  });

  it("returns null when a counter rolls back", () => {
    expect(computeRate({ t: 1_000, rx: 1_000, tx: 1_000, uptime: 10 }, { t: 2_000, rx: 500, tx: 1_500, uptime: 11 })).toBeNull();
    expect(computeRate({ t: 1_000, rx: 1_000, tx: 1_000, uptime: 10 }, { t: 2_000, rx: 1_500, tx: 500, uptime: 11 })).toBeNull();
  });
});

describe("activityWindow", () => {
  const prev = activity({ seq: 1, observed_ms: 1_000, rx_bytes: 1_000, tx_bytes: 500 });

  it("returns null for the first sample", () => {
    expect(activityWindow(null, prev)).toBeNull();
  });

  it("divides sample bytes by the proven window", () => {
    const window = activityWindow(prev, activity({ seq: 2, observed_ms: 6_000, rx_bytes: 1_000, tx_bytes: 500 }));
    expect(window).toEqual({ dtMs: 5_000, rxPerSec: 200, txPerSec: 100 });
  });

  it("returns null when seq skips", () => {
    expect(activityWindow(prev, activity({ seq: 3, observed_ms: 6_000 }))).toBeNull();
  });

  it("returns null when the window exceeds two cadences", () => {
    expect(activityWindow(prev, activity({ seq: 2, observed_ms: 12_001 }))).toBeNull();
    expect(activityWindow(prev, activity({ seq: 2, observed_ms: 11_000 }))).not.toBeNull();
  });

  it("returns null when observed time does not advance", () => {
    expect(activityWindow(prev, activity({ seq: 2, observed_ms: 1_000 }))).toBeNull();
    expect(activityWindow(prev, activity({ seq: 2, observed_ms: 900 }))).toBeNull();
  });

  it("returns null across different links", () => {
    expect(activityWindow(prev, activity({ origin: "other", seq: 2, observed_ms: 6_000 }))).toBeNull();
  });
});

describe("buildMesh", () => {
  const now = 10_000;

  it("adds no edge for a non-direct peer without route or activity", () => {
    const model = buildMesh("self", [peer("p1", { direct: false })], [], [], now);
    expect(model.edges).toEqual([]);
    expect(model.nodes.map((n) => n.id)).toContain("p1");
  });

  it("adds a direct edge for a direct, up peer", () => {
    const model = buildMesh("self", [peer("p1", { direct: true, rtt_ms: 12 })], [], [], now);
    expect(model.edges).toEqual([
      { id: "self>p1", from: "self", to: "p1", evidence: "direct", fresh: true, rttMs: 12 },
    ]);
  });

  it("adds no direct edge for a direct peer that is not up", () => {
    const model = buildMesh("self", [peer("p1", { direct: true, state: "down" })], [], [], now);
    expect(model.edges).toEqual([]);
  });

  it("walks a route path from self", () => {
    const model = buildMesh("self", [], [route("b", ["a", "b"])], [], now);
    expect(model.edges).toEqual([
      { id: "self>a", from: "self", to: "a", evidence: "route", fresh: false },
      { id: "a>b", from: "a", to: "b", evidence: "route", fresh: false },
    ]);
    expect(model.nodes.find((n) => n.id === "a")).toMatchObject({ kind: "intermediate", state: "unknown" });
    expect(model.nodes.map((n) => n.id)).toEqual(expect.arrayContaining(["self", "a", "b"]));
  });

  it("keeps the route destination node even with an empty path", () => {
    const model = buildMesh("self", [], [route("b", [], { next_hop: "b", hops: 1 })], [], now);
    expect(model.nodes.map((n) => n.id)).toContain("b");
    expect(model.edges).toEqual([{ id: "self>b", from: "self", to: "b", evidence: "route", fresh: false }]);
  });

  it("prefers direct over route for the same edge", () => {
    const model = buildMesh("self", [peer("a", { direct: true })], [route("b", ["a", "b"])], [], now);
    const edges = model.edges.filter((e) => e.from === "self" && e.to === "a");
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({ evidence: "direct", fresh: true });
  });

  it("adds an observed edge from self activity", () => {
    const model = buildMesh(
      "self",
      [],
      [],
      [activity({ origin: "self", destination: "dest", path: ["dest"], rtt_ms: 8 })],
      now,
    );
    expect(model.edges).toEqual([
      { id: "self>dest", from: "self", to: "dest", evidence: "observed", fresh: true, rttMs: 8 },
    ]);
  });

  it("adds an observed edge between relay hops, dropping the duplicated origin", () => {
    const model = buildMesh("self", [], [], [activity({ origin: "a", destination: "b", path: ["a", "b"] })], now);
    expect(model.edges).toEqual([{ id: "a>b", from: "a", to: "b", evidence: "observed", fresh: true }]);
  });

  it("prefers observed over route for the same edge", () => {
    const model = buildMesh("self", [], [route("a", ["a"])], [activity({ origin: "self", path: ["a"] })], now);
    expect(model.edges).toEqual([{ id: "self>a", from: "self", to: "a", evidence: "observed", fresh: true }]);
  });

  it("ignores expired activity", () => {
    const model = buildMesh("self", [], [], [activity({ origin: "self", path: ["dest"], expires_ms: now })], now);
    expect(model.edges).toEqual([]);
  });

  it("maps routes to paths", () => {
    const model = buildMesh("self", [], [route("b", ["a", "b"])], [], now);
    expect(model.paths).toEqual([{ destination: "b", nextHop: "a", path: ["a", "b"], hops: 2, costMs: 10 }]);
  });

  it("flags stale when every peer row is older than 15 s", () => {
    expect(buildMesh("self", [peer("p1", { last_seen_ms: 20_000 })], [], [], now).stale).toBe(true);
    expect(buildMesh("self", [peer("p1", { last_seen_ms: 5_000 })], [], [], now).stale).toBe(false);
    expect(
      buildMesh("self", [peer("p1", { last_seen_ms: 20_000 }), peer("p2", { last_seen_ms: 5_000 })], [], [], now).stale,
    ).toBe(false);
    expect(buildMesh("self", [], [], [], now).stale).toBe(false);
  });
});

describe("dedupeActivities", () => {
  it("collapses the same origin/destination/seq to the newest sample", () => {
    const rows = [
      activity({ seq: 1, observed_ms: 100, rx_bytes: 10 }),
      activity({ seq: 1, observed_ms: 300, rx_bytes: 30 }),
      activity({ seq: 1, observed_ms: 200, rx_bytes: 20 }),
      activity({ seq: 2, observed_ms: 400, rx_bytes: 40 }),
    ];
    const out = dedupeActivities(rows);
    expect(out).toHaveLength(2);
    expect(out.find((r) => r.seq === 1)).toMatchObject({ observed_ms: 300, rx_bytes: 30 });
    expect(out.find((r) => r.seq === 2)).toMatchObject({ observed_ms: 400 });
  });
});

describe("bucketActivities", () => {
  it("sums bytes in one bucket and keeps the latest RTT", () => {
    const rows = [
      activity({ observed_ms: 1_000, rx_bytes: 100, tx_bytes: 10, rtt_ms: 20, jitter_ms: 4 }),
      activity({ observed_ms: 30_000, rx_bytes: 200, tx_bytes: 20, rtt_ms: 12, jitter_ms: 2 }),
      activity({ observed_ms: 61_000, rx_bytes: 7, tx_bytes: 3, rtt_ms: 30 }),
    ];
    const buckets = bucketActivities(rows, 60_000);
    expect(buckets).toHaveLength(2);
    expect(buckets[0]).toMatchObject({ rx: 300, tx: 30, rttMs: 12, jitterMs: 2, lastObservedMs: 30_000, samples: 2 });
    expect(buckets[0].key).toContain("self>p1");
    expect(buckets[1]).toMatchObject({ rx: 7, tx: 3, samples: 1, lastObservedMs: 61_000 });
  });
});

describe("presenceOf", () => {
  const now = 100_000;

  it("is active with fresh traffic", () => {
    expect(presenceOf(peer("p1"), now - 1_000, now)).toBe("active");
  });

  it("is idle for an up peer without fresh traffic", () => {
    expect(presenceOf(peer("p1"), undefined, now)).toBe("idle");
    expect(presenceOf(peer("p1"), now - 60_000, now)).toBe("idle");
  });

  it("is offline when the peer is not up", () => {
    expect(presenceOf(peer("p1", { state: "down" }), now - 1_000, now)).toBe("offline");
    expect(presenceOf(peer("p1", { state: "connecting" }), now - 1_000, now)).toBe("offline");
  });

  it("is unknown without peer details", () => {
    expect(presenceOf(undefined, undefined, now)).toBe("unknown");
    expect(presenceOf(null, undefined, now)).toBe("unknown");
  });
});

describe("peerHealth", () => {
  it("reports an up direct peer", () => {
    const health = peerHealth(peer("p1", { direct: true, rtt_ms: 12 }), "active");
    expect(health).toMatchObject({ tone: "ok", label: "Up" });
    expect(health.detail).toContain("Direct");
  });

  it("reports an up routed peer", () => {
    const health = peerHealth(peer("p1", { direct: false }), "active");
    expect(health.tone).toBe("ok");
    expect(health.detail).toContain("route");
  });

  it("reports a down peer with last-seen age", () => {
    const health = peerHealth(peer("p1", { state: "down", last_seen_ms: 90_000 }), "offline");
    expect(health).toMatchObject({ tone: "down", label: "Offline" });
    expect(health.detail).toContain("Last seen 2 min");
  });

  it("reports unknown without details", () => {
    expect(peerHealth(undefined, "unknown").tone).toBe("unknown");
    expect(peerHealth(null, "unknown").tone).toBe("unknown");
  });
});

describe("formatRate", () => {
  it("shows a dash for unknown rates", () => {
    expect(formatRate(null)).toBe("—");
  });

  it("formats B/s, kB/s and MB/s with one decimal", () => {
    expect(formatRate({ rxPerSec: 512, txPerSec: 0 })).toBe("512.0 B/s");
    expect(formatRate({ rxPerSec: 600, txPerSec: 600 })).toBe("1.2 kB/s");
    expect(formatRate({ rxPerSec: 1_500_000, txPerSec: 0 })).toBe("1.5 MB/s");
    expect(formatRate({ rxPerSec: 0, txPerSec: 0 })).toBe("0.0 B/s");
  });
});

describe("formatBytesPerSec", () => {
  it("shows a dash for invalid or negative values", () => {
    expect(formatBytesPerSec(Number.NaN)).toBe("—");
    expect(formatBytesPerSec(-1)).toBe("—");
    expect(formatBytesPerSec(Number.POSITIVE_INFINITY)).toBe("—");
  });

  it("formats one direction without summing", () => {
    expect(formatBytesPerSec(0)).toBe("0.0 B/s");
    expect(formatBytesPerSec(512)).toBe("512.0 B/s");
    expect(formatBytesPerSec(1_500)).toBe("1.5 kB/s");
    expect(formatBytesPerSec(2_500_000)).toBe("2.5 MB/s");
  });
});

describe("formatAge", () => {
  it("shows a dash when the age is unknown", () => {
    expect(formatAge(undefined)).toBe("—");
  });

  it("formats seconds, minutes, hours and days", () => {
    expect(formatAge(500)).toBe("<1 s");
    expect(formatAge(5_000)).toBe("5 s");
    expect(formatAge(90_000)).toBe("2 min");
    expect(formatAge(7_200_000)).toBe("2 h");
    expect(formatAge(172_800_000)).toBe("2 d");
  });
});
