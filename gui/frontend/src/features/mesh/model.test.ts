import { describe, expect, it } from "vitest";
import type { Activity, Peer, Route } from "@/lib/api";
import {
  activityWindow,
  buildMesh,
  bucketActivities,
  computeRate,
  dedupeActivities,
  facingSides,
  formatAge,
  formatBytesPerSec,
  formatRate,
  linkFamily,
  peerHealth,
  presenceOf,
  routeSummary,
  shouldFlow,
  type MeshPath,
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

const meshPath = (over: Partial<MeshPath> = {}): MeshPath => ({
  destination: "dest",
  nextHop: "relay",
  path: ["relay", "dest"],
  hops: 2,
  costMs: 10,
  direct: false,
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

it("attaches straight links to facing card sides in every direction", () => {
  expect(facingSides(0, -150)).toEqual(["top", "bottom"]);
  expect(facingSides(0, 150)).toEqual(["bottom", "top"]);
  expect(facingSides(150, 0)).toEqual(["right", "left"]);
  expect(facingSides(-150, 0)).toEqual(["left", "right"]);
  expect(facingSides(150, -40)).toEqual(["right", "left"]);
  expect(facingSides(-40, 150)).toEqual(["bottom", "top"]);
  expect(facingSides(150, -150)).toEqual(["top", "bottom"]);
  expect(facingSides(-150, -150)).toEqual(["top", "bottom"]);
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

describe("shouldFlow", () => {
  const rate = { rxPerSec: 0, txPerSec: 1 };

  it("flows for direct or observed evidence with a positive proven rate", () => {
    expect(shouldFlow("direct", rate, true)).toBe(true);
    expect(shouldFlow("observed", rate, true)).toBe(true);
    expect(shouldFlow("observed", { rxPerSec: 2, txPerSec: 0 }, true)).toBe(true);
  });

  it("never flows route evidence", () => {
    expect(shouldFlow("route", rate, true)).toBe(false);
  });

  it("never flows with an absent or zero rate", () => {
    expect(shouldFlow("direct", null, true)).toBe(false);
    expect(shouldFlow("direct", undefined, true)).toBe(false);
    expect(shouldFlow("observed", { rxPerSec: 0, txPerSec: 0 }, true)).toBe(false);
  });

  it("never flows when motion is reduced", () => {
    expect(shouldFlow("direct", rate, false)).toBe(false);
    expect(shouldFlow("observed", rate, false)).toBe(false);
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
    const model = buildMesh("self", [peer("p1", { direct: false })], [], [], [], now);
    expect(model.edges).toEqual([]);
    expect(model.nodes.map((n) => n.id)).toContain("p1");
  });

  it("adds a direct edge for a direct, up peer", () => {
    const model = buildMesh("self", [peer("p1", { direct: true, rtt_ms: 12 })], [], [], [], now);
    expect(model.edges).toEqual([
      { id: "self>p1", from: "self", to: "p1", evidence: "direct", fresh: true, rttMs: 12 },
    ]);
  });

  it("adds no direct edge for a direct peer that is not up", () => {
    const model = buildMesh("self", [peer("p1", { direct: true, state: "down" })], [], [], [], now);
    expect(model.edges).toEqual([]);
  });

  it("walks a route path from self", () => {
    const model = buildMesh("self", [], [route("b", ["a", "b"])], [], [], now);
    expect(model.edges).toEqual([
      { id: "self>a", from: "self", to: "a", evidence: "route", fresh: false },
      { id: "a>b", from: "a", to: "b", evidence: "route", fresh: false },
    ]);
    expect(model.nodes.find((n) => n.id === "a")).toMatchObject({ kind: "intermediate", state: "unknown" });
    expect(model.nodes.map((n) => n.id)).toEqual(expect.arrayContaining(["self", "a", "b"]));
  });

  it("keeps the route destination node even with an empty path", () => {
    const model = buildMesh("self", [], [route("b", [], { next_hop: "b", hops: 1 })], [], [], now);
    expect(model.nodes.map((n) => n.id)).toContain("b");
    expect(model.edges).toEqual([{ id: "self>b", from: "self", to: "b", evidence: "route", fresh: false }]);
  });

  it("prefers direct over route for the same edge", () => {
    const model = buildMesh("self", [peer("a", { direct: true })], [route("b", ["a", "b"])], [], [], now);
    const edges = model.edges.filter((e) => e.from === "self" && e.to === "a");
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({ evidence: "direct", fresh: true });
  });

  it("adds an observed edge from self activity", () => {
    const model = buildMesh(
      "self",
      [],
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
    const model = buildMesh("self", [], [], [], [activity({ origin: "a", destination: "b", path: ["a", "b"] })], now);
    expect(model.edges).toEqual([{ id: "a>b", from: "a", to: "b", evidence: "observed", fresh: true }]);
  });

  it("prefers observed over route for the same edge", () => {
    const model = buildMesh("self", [], [route("a", ["a"])], [], [activity({ origin: "self", path: ["a"] })], now);
    expect(model.edges).toEqual([{ id: "self>a", from: "self", to: "a", evidence: "observed", fresh: true }]);
  });

  it("ignores expired activity", () => {
    const model = buildMesh("self", [], [], [], [activity({ origin: "self", path: ["dest"], expires_ms: now })], now);
    expect(model.edges).toEqual([]);
  });

  it("draws a neighbour's direct advertisement as a peer link", () => {
    const model = buildMesh(
      "self",
      [peer("a", { direct: false }), peer("c", { direct: false })],
      [],
      [{ from: "a", destination: "c", hops: 1, cost_ms: 5, path: ["a", "c"] }],
      [],
      now,
    );
    expect(model.edges).toEqual([
      expect.objectContaining({ id: "a>c", from: "a", to: "c", evidence: "advertised", rttMs: 5, path: ["a", "c"], hops: 1 }),
    ]);
  });

  it("walks a multi-hop advertisement without inventing an RTT", () => {
    const model = buildMesh(
      "self",
      [],
      [],
      [{ from: "a", destination: "c", hops: 2, cost_ms: 9, path: ["a", "b", "c"] }],
      [],
      now,
    );
    expect(model.edges.map((e) => e.id)).toEqual(["a>b", "b>c"]);
    expect(model.edges.every((e) => e.evidence === "advertised" && e.rttMs === undefined)).toBe(true);
  });

  it("maps routes to paths", () => {
    const model = buildMesh("self", [], [route("b", ["a", "b"])], [], [], now);
    expect(model.paths).toEqual([{ destination: "b", nextHop: "a", path: ["a", "b"], hops: 2, costMs: 10, direct: false }]);
  });

  it("marks a direct route and the family its first-hop link observed", () => {
    const model = buildMesh(
      "self",
      [peer("p1", { direct: true, path: "v6", endpoint: "[2001:db8::1]:51820" })],
      [route("p1", ["p1"])],
      [],
      [],
      now,
    );
    expect(model.paths[0]).toMatchObject({ destination: "p1", nextHop: "p1", direct: true, family: "IPv6" });
  });

  it("keeps the relayed selected route when the destination also has a direct link", () => {
    const model = buildMesh(
      "self",
      [peer("relay", { direct: true, path: "v4" }), peer("dest", { direct: true })],
      [route("dest", ["relay", "dest"])],
      [],
      [],
      now,
    );
    expect(model.paths[0]).toMatchObject({ destination: "dest", nextHop: "relay", direct: false });
    expect(model.edges).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: "self>dest", evidence: "direct" }),
        expect.objectContaining({ id: "self>relay", evidence: "direct" }),
        expect.objectContaining({ id: "relay>dest", evidence: "route" }),
      ]),
    );
  });

  it("flags stale when every peer row is older than 15 s", () => {
    expect(buildMesh("self", [peer("p1", { last_seen_ms: 20_000 })], [], [], [], now).stale).toBe(true);
    expect(buildMesh("self", [peer("p1", { last_seen_ms: 5_000 })], [], [], [], now).stale).toBe(false);
    expect(
      buildMesh("self", [peer("p1", { last_seen_ms: 20_000 }), peer("p2", { last_seen_ms: 5_000 })], [], [], [], now).stale,
    ).toBe(false);
    expect(buildMesh("self", [], [], [], [], now).stale).toBe(false);
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

  it("is offline only when the peer reports down", () => {
    expect(presenceOf(peer("p1", { state: "down" }), now - 1_000, now)).toBe("offline");
    expect(presenceOf(peer("p1", { state: "stale" }), now - 1_000, now)).toBe("unknown");
    expect(presenceOf(peer("p1", { state: "degraded" }), now - 1_000, now)).toBe("unknown");
  });

  it("is routed, not offline, when a selected route exists without a direct link", () => {
    expect(presenceOf(peer("p1", { state: "down" }), now - 1_000, now, true)).toBe("routed");
  });

  it("is unknown without peer details", () => {
    expect(presenceOf(undefined, undefined, now)).toBe("unknown");
    expect(presenceOf(null, undefined, now)).toBe("unknown");
  });
});

describe("linkFamily", () => {
  it("reads the family of an observed direct link", () => {
    expect(linkFamily(peer("p1", { direct: true, path: "v4" }))).toBe("IPv4");
    expect(linkFamily(peer("p1", { direct: true, path: "v6" }))).toBe("IPv6");
    expect(linkFamily(peer("p1", { direct: true, path: "", endpoint: "198.51.100.7:51820" }))).toBe("IPv4");
    expect(linkFamily(peer("p1", { direct: true, path: "", endpoint: "[2001:db8::1]:51820" }))).toBe("IPv6");
  });

  it("never guesses from an unobserved link or an overlay address", () => {
    expect(linkFamily(peer("p1", { direct: false, path: "v6" }))).toBeUndefined();
    expect(linkFamily(peer("p1", { direct: true, overlay_ipv6: "fd00::1" }))).toBeUndefined();
    expect(linkFamily(undefined)).toBeUndefined();
  });
});

describe("routeSummary", () => {
  const nameOf = (id: string) => (id === "relay" ? "Relay" : id);

  it("names the selected route kind", () => {
    expect(routeSummary(meshPath({ direct: true, nextHop: "dest", family: "IPv6" }), nameOf)).toBe("direct · IPv6");
    expect(routeSummary(meshPath({ direct: true, nextHop: "dest" }), nameOf)).toBe("direct");
    expect(routeSummary(meshPath(), nameOf)).toBe("via Relay");
  });

  it("says no route when the daemon resolved none", () => {
    expect(routeSummary(undefined, nameOf)).toBe("no route");
  });
});

describe("peerHealth", () => {
  it("reports an up direct peer", () => {
    const health = peerHealth(peer("p1", { direct: true, rtt_ms: 12 }), "active");
    expect(health).toMatchObject({ tone: "ok", label: "Up" });
    expect(health.detail).toContain("Direct");
  });

  it("reports an up peer without a direct link without claiming a relay", () => {
    const health = peerHealth(peer("p1", { direct: false }), "active");
    expect(health.tone).toBe("ok");
    expect(health.detail).toBe("No direct link");
  });

  it("reports a routed peer without claiming an active session", () => {
    const health = peerHealth(peer("p1", { state: "down" }), "routed");
    expect(health).toMatchObject({ tone: "warn", label: "Routed", detail: "No direct link · route selected" });
  });

  it("reports a down peer as no route, with last-seen age", () => {
    const health = peerHealth(peer("p1", { state: "down", last_seen_ms: 90_000 }), "offline");
    expect(health).toMatchObject({ tone: "down", label: "No route" });
    expect(health.detail).toContain("Last seen 2 min");
  });

  it("reports an unknown non-up state without claiming offline", () => {
    expect(peerHealth(peer("p1", { state: "stale" }), "unknown")).toMatchObject({
      tone: "unknown",
      label: "Unknown",
      detail: "State stale",
    });
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
