import type { Activity, Link, Peer, Route } from "@/lib/api";

export type CounterSample = { t: number; rx: number; tx: number; uptime: number };
export type Rate = { rxPerSec: number; txPerSec: number };
export type HistorySample = { t: number; rxPerSec: number; txPerSec: number; peersUp: number; peersTotal: number };
export type Evidence = "direct" | "observed" | "advertised" | "route";
export type MeshNode = {
  id: string;
  label: string;
  overlay?: string;
  kind: "self" | "peer" | "intermediate";
  state: string;
  rttMs?: number;
  transport?: string;
  hops?: number;
};
export type MeshEdge = {
  id: string;
  from: string;
  to: string;
  evidence: Evidence;
  fresh: boolean;
  rttMs?: number;
  /** Full path of a neighbour advertisement, starting at the advertiser. */
  path?: string[];
  hops?: number;
  costMs?: number;
};
export type MeshPath = {
  destination: string;
  nextHop: string;
  path: string[];
  hops: number;
  costMs: number;
  /** The selected route is a one-hop direct link, not a relayed advertisement. */
  direct: boolean;
  /** Underlay family of the first hop, only when that direct link is observed. */
  family?: "IPv4" | "IPv6";
};
export type MeshModel = { nodes: MeshNode[]; edges: MeshEdge[]; paths: MeshPath[]; stale: boolean };
export type Presence = "active" | "idle" | "routed" | "offline" | "unknown";
export type PeerHealth = { tone: "ok" | "warn" | "down" | "unknown"; label: string; detail: string };

export type ActivityBucket = {
  key: string;
  origin: string;
  destination: string;
  transport: string;
  direct: boolean;
  path: string[];
  rx: number;
  tx: number;
  rttMs: number;
  jitterMs: number;
  lastObservedMs: number;
  samples: number;
};

/** Nominal activity cadence; a remote rate needs two samples no further apart than two cadences. */
export const StateInterval = 5_000;

const StaleAfterMs = 15_000;
const ActiveWindowMs = 10_000;

/** Attach a straight link to the two card sides facing one another. */
export function facingSides(dx: number, dy: number): [string, string] {
  // Prefer the vertical handles when the axes are close. At a 45° fan the
  // floating-point difference between dx and dy would otherwise give two
  // symmetric peers different attachment sides.
  if (Math.abs(dx) > Math.abs(dy) * 1.2) return dx > 0 ? ["right", "left"] : ["left", "right"];
  return dy > 0 ? ["bottom", "top"] : ["top", "bottom"];
}

// computeRate turns two cumulative counter samples into per-second rates.
// A null result means the window proves nothing: no previous sample, no
// elapsed time, a daemon restart (uptime went backwards) or a counter rollback.
export function computeRate(prev: CounterSample | null, next: CounterSample): Rate | null {
  if (!prev) return null;
  const dtMs = next.t - prev.t;
  if (dtMs <= 0) return null;
  if (next.uptime < prev.uptime) return null;
  if (next.rx < prev.rx || next.tx < prev.tx) return null;
  return {
    rxPerSec: ((next.rx - prev.rx) * 1000) / dtMs,
    txPerSec: ((next.tx - prev.tx) * 1000) / dtMs,
  };
}

// activityWindow is computeRate for the mesh's per-link samples. The bytes in
// an Activity are a delta over an unexported window, so a rate is only proven
// by consecutive seq numbers on the same link and a short, positive gap.
export function activityWindow(
  prev: Activity | null,
  next: Activity,
): { dtMs: number; rxPerSec: number; txPerSec: number } | null {
  if (!prev) return null;
  if (prev.origin !== next.origin || prev.destination !== next.destination) return null;
  if (next.seq !== prev.seq + 1) return null;
  const dtMs = next.observed_ms - prev.observed_ms;
  if (dtMs <= 0 || dtMs > 2 * StateInterval) return null;
  return {
    dtMs,
    rxPerSec: (next.rx_bytes * 1000) / dtMs,
    txPerSec: (next.tx_bytes * 1000) / dtMs,
  };
}

// shouldFlow gates the link flow marker: route segments never animate, and
// motion needs both permission and a proven positive rate in either direction.
export function shouldFlow(evidence: Evidence, rate: Rate | null | undefined, motionAllowed: boolean): boolean {
  if (!motionAllowed || evidence === "route" || evidence === "advertised") return false;
  return rate != null && rate.rxPerSec + rate.txPerSec > 0;
}

const evidenceRank: Record<Evidence, number> = { route: 0, advertised: 1, observed: 2, direct: 3 };

// linkFamily reports the underlay address family of a peer's observed direct
// link. peer.path is the daemon's "v4"/"v6" link path; the endpoint carries
// the same fact. Candidates and overlay addresses are not evidence of the
// active link, so an unobserved link stays unknown.
export function linkFamily(peer: Peer | null | undefined): "IPv4" | "IPv6" | undefined {
  if (!peer || !peer.direct) return undefined;
  if (peer.path === "v4") return "IPv4";
  if (peer.path === "v6") return "IPv6";
  const endpoint = peer.endpoint?.trim();
  if (!endpoint) return undefined;
  const close = endpoint.indexOf("]");
  const host = endpoint.startsWith("[") && close >= 0 ? endpoint.slice(1, close) : endpoint.replace(/:\d+$/, "");
  if (host.includes(":")) return "IPv6";
  if (host.includes(".")) return "IPv4";
  return undefined;
}

// routeSummary renders one selected route without inventing reachability:
// a direct route names its family when the first-hop link observed it, a
// relayed route names the relay, and an absent route says so.
export function routeSummary(
  route: MeshPath | undefined,
  nameOf: (id: string) => string,
): string {
  if (!route) return "no route";
  if (route.direct) return route.family ? `direct · ${route.family}` : "direct";
  return `via ${nameOf(route.nextHop)}`;
}

export function buildMesh(
  self: string,
  peers: Peer[],
  routes: Route[],
  links: Link[],
  activities: Activity[],
  now: number,
): MeshModel {
  const nodes = new Map<string, MeshNode>();
  const addNode = (node: MeshNode) => {
    if (!nodes.has(node.id)) nodes.set(node.id, node);
  };

  addNode({ id: self, label: "This device", kind: "self", state: "up" });
  for (const p of peers) {
    addNode({
      id: p.id,
      label: p.name || p.id,
      overlay: p.overlay_ipv4 || p.overlay_ipv6 || undefined,
      kind: "peer",
      state: p.state,
      rttMs: p.rtt_ms > 0 ? p.rtt_ms : undefined,
      transport: p.transport || undefined,
    });
  }
  // A hop that is not in the peer table is still on the mesh: show it as an
  // intermediate instead of dropping the path or inventing peer detail.
  const ensureNode = (id: string) => {
    if (!id) return;
    addNode({ id, label: id, kind: "intermediate", state: "unknown" });
  };

  const edges = new Map<string, MeshEdge>();
  const addEdge = (from: string, to: string, evidence: Evidence, rttMs?: number) => {
    if (!from || !to || from === to) return;
    const id = `${from}>${to}`;
    const prev = edges.get(id);
    if (prev && evidenceRank[prev.evidence] >= evidenceRank[evidence]) return;
    const edge: MeshEdge = { id, from, to, evidence, fresh: evidence !== "route" };
    if (rttMs !== undefined && rttMs > 0) edge.rttMs = rttMs;
    edges.set(id, edge);
  };

  // Direct evidence is a live, authenticated peer row only.
  for (const p of peers) {
    if (p.direct && p.state === "up") addEdge(self, p.id, "direct", p.rtt_ms);
  }

  const paths: MeshPath[] = [];
  for (const r of routes) {
    const path = r.path ?? [];
    ensureNode(r.destination);
    const family = linkFamily(peers.find((p) => p.id === r.next_hop));
    paths.push({
      destination: r.destination,
      nextHop: r.next_hop,
      path,
      hops: r.hops,
      costMs: r.cost_ms,
      // A direct route's next hop is its destination; an advertised route's
      // next hop is a relay even when a direct link to the destination exists.
      direct: r.next_hop === r.destination,
      ...(family ? { family } : {}),
    });
    // The destination is always reachable from self: an empty path means the
    // route's next hop is the destination itself.
    const chain = [self, ...(path.length > 0 ? path : [r.destination])];
    for (let i = 0; i < chain.length; i++) ensureNode(chain[i]);
    for (let i = 0; i < chain.length - 1; i++) addEdge(chain[i], chain[i + 1], "route");
  }

  // A neighbour's advertisement is the only evidence of how two other
  // members reach each other. A one-hop advertisement is a direct peer
  // link; a longer path is a claimed chain. Local routes already describe
  // this node's own paths, so self-originated advertisements are skipped.
  for (const l of links) {
    if (!l.from || l.from === self) continue;
    const path = l.path && l.path.length > 0 ? l.path : [l.from, l.destination];
    const chain = path[0] === l.from ? path : [l.from, ...path];
    for (const id of chain) ensureNode(id);
    for (let i = 0; i < chain.length - 1; i++) {
      addEdge(chain[i], chain[i + 1], "advertised", l.hops === 1 ? l.cost_ms : undefined);
      const edge = edges.get(`${chain[i]}>${chain[i + 1]}`);
      if (edge && edge.evidence === "advertised") {
        edge.path = chain;
        edge.hops = l.hops;
        edge.costMs = l.cost_ms;
      }
    }
  }

  // Observed evidence expires: a stale sample proves nothing about the link now.
  for (const a of activities) {
    if (a.expires_ms <= now) continue;
    const chain = a.path && a.path.length > 0 ? [a.origin, ...a.path] : [a.origin, a.destination];
    if (chain[1] === a.origin) chain.splice(1, 1);
    for (let i = 0; i < chain.length; i++) ensureNode(chain[i]);
    for (let i = 0; i < chain.length - 1; i++) addEdge(chain[i], chain[i + 1], "observed", a.rtt_ms);
  }

  // last_seen_ms is elapsed age, not an epoch: if every known peer was last
  // seen over 15 s ago, the peer snapshot itself is old.
  const stale = peers.length > 0 && peers.every((p) => p.last_seen_ms > StaleAfterMs);

  return { nodes: [...nodes.values()], edges: [...edges.values()], paths, stale };
}

export function dedupeActivities(rows: Activity[]): Activity[] {
  const byKey = new Map<string, Activity>();
  for (const row of rows) {
    const key = `${row.origin}|${row.destination}|${row.seq}`;
    const prev = byKey.get(key);
    if (!prev || row.observed_ms >= prev.observed_ms) byKey.set(key, row);
  }
  return [...byKey.values()];
}

export function bucketActivities(rows: Activity[], windowMs: number): ActivityBucket[] {
  const span = windowMs > 0 ? windowMs : 1;
  const buckets = new Map<string, ActivityBucket & { start: number }>();
  for (const row of rows) {
    const start = Math.floor(row.observed_ms / span) * span;
    const key = `${row.origin}>${row.destination}@${start}`;
    const bucket = buckets.get(key);
    if (!bucket) {
      buckets.set(key, {
        key,
        start,
        origin: row.origin,
        destination: row.destination,
        transport: row.transport,
        direct: row.direct,
        path: row.path ?? [],
        rx: row.rx_bytes,
        tx: row.tx_bytes,
        rttMs: row.rtt_ms,
        jitterMs: row.jitter_ms,
        lastObservedMs: row.observed_ms,
        samples: 1,
      });
      continue;
    }
    bucket.rx += row.rx_bytes;
    bucket.tx += row.tx_bytes;
    bucket.samples += 1;
    if (row.observed_ms >= bucket.lastObservedMs) {
      bucket.lastObservedMs = row.observed_ms;
      bucket.rttMs = row.rtt_ms;
      bucket.jitterMs = row.jitter_ms;
      bucket.transport = row.transport;
      bucket.direct = row.direct;
      bucket.path = row.path ?? [];
    }
  }
  return [...buckets.values()]
    .sort((a, b) => a.start - b.start || a.key.localeCompare(b.key))
    .map(({ start: _start, ...bucket }) => bucket);
}

export function presenceOf(
  peer: Peer | null | undefined,
  lastTrafficAt: number | undefined,
  now: number,
  routed = false,
): Presence {
  if (!peer || !peer.id) return "unknown";
  if (peer.state !== "up") {
    if (routed) return "routed";
    // A down direct link is not evidence that the remote machine is offline.
    return peer.state === "down" ? "offline" : "unknown";
  }
  if (lastTrafficAt !== undefined && now - lastTrafficAt <= ActiveWindowMs) return "active";
  return "idle";
}

export function peerHealth(peer: Peer | null | undefined, presence: Presence): PeerHealth {
  if (!peer || !peer.id) {
    return { tone: "unknown", label: "Unknown", detail: "No peer details" };
  }
  if (presence === "unknown") {
    return { tone: "unknown", label: "Unknown", detail: peer.state ? `State ${peer.state}` : "No state reported" };
  }
  if (presence === "offline") {
    return {
      tone: "down",
      label: "No route",
      detail: peer.last_seen_ms > 0 ? `Last seen ${formatAge(peer.last_seen_ms)} ago` : "Never observed",
    };
  }
  if (presence === "routed") {
    // A selected route is resolved state, not proof of an active session.
    return { tone: "warn", label: "Routed", detail: "No direct link · route selected" };
  }
  if (presence === "idle") return { tone: "ok", label: "Idle", detail: "Up, no recent traffic" };
  if (peer.direct) {
    return {
      tone: "ok",
      label: "Up",
      detail: peer.rtt_ms > 0 ? `Direct · ${Math.round(peer.rtt_ms)} ms` : "Direct link",
    };
  }
  // A peer without a direct link may be relayed or simply offline; this
  // function only sees link state, so it must not claim a relay.
  return { tone: "ok", label: "Up", detail: "No direct link" };
}

// formatBytesPerSec renders one direction. Summing RX and TX is only right
// for a combined total; arrows and inspectors need each direction separately.
export function formatBytesPerSec(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1_000) return `${n.toFixed(1)} B/s`;
  if (n < 1_000_000) return `${(n / 1_000).toFixed(1)} kB/s`;
  return `${(n / 1_000_000).toFixed(1)} MB/s`;
}

export function formatRate(rate: Rate | null): string {
  if (!rate) return "—";
  return formatBytesPerSec(rate.rxPerSec + rate.txPerSec);
}

export function formatAge(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1_000) return "<1 s";
  const seconds = Math.round(ms / 1_000);
  if (seconds < 60) return `${seconds} s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} h`;
  return `${Math.round(hours / 24)} d`;
}
