import type { LogEntry, Peer, Route } from "@/lib/api";
import { shortID } from "@/lib/utils";

/**
 * Palette commands are built by a pure function so the action set is testable
 * without a DOM. Two rules keep the palette honest:
 *
 * - A non-destructive action calls the same binding the owning page calls.
 * - A destructive action (rotate, reset) navigates to the page that owns its
 *   confirmation instead of growing a second copy of that dialog here.
 */
export type CommandGroup = "navigate" | "action" | "peer" | "route" | "log";

export type PaletteCommand = {
  id: string;
  group: CommandGroup;
  label: string;
  hint: string;
  run: () => void;
};

export type PaletteContext = {
  peers: Peer[];
  routes: Route[];
  logs: LogEntry[];
  /** status.connection.state, or "unknown" before the first snapshot. */
  connection: string;
  departed: boolean;
  navigate: (to: string) => void;
  connect: () => void;
  disconnect: () => void;
  startService: () => void;
  stopService: () => void;
  restartService: () => void;
  probe: (peerId: string) => void;
  ping: (peer: Peer) => void;
};

const destinations: ReadonlyArray<{ label: string; to: string }> = [
  { label: "Overview", to: "/" },
  { label: "Network", to: "/network" },
  { label: "Peers", to: "/peers" },
  { label: "Invitations", to: "/invites" },
  { label: "Security", to: "/security" },
  { label: "Activity", to: "/activity" },
  { label: "Diagnostics", to: "/logs" },
  { label: "Settings", to: "/settings" },
  { label: "About", to: "/about" },
];

/** Log results only appear once the query can actually narrow them. */
const LogQueryMin = 2;
const LogResultsMax = 5;
const RouteResultsMax = 8;

/**
 * fuzzyScore returns null when the query is not a subsequence of the text, and
 * a larger score the better the match: a contiguous substring beats scattered
 * characters, an earlier match beats a later one, and word starts count extra.
 */
export function fuzzyScore(query: string, text: string): number | null {
  const q = query.trim().toLowerCase();
  if (!q) return 0;
  const t = text.toLowerCase();
  const direct = t.indexOf(q);
  if (direct >= 0) return 1000 - Math.min(direct, 999);

  let at = 0;
  let prev = -2;
  let score = 0;
  for (const ch of q) {
    const found = t.indexOf(ch, at);
    if (found < 0) return null;
    const boundary = found === 0 || /[\s\-_/·:.|]/.test(t[found - 1]);
    const adjacent = found === prev + 1;
    score += 1 + (boundary ? 6 : 0) + (adjacent ? 4 : 0);
    prev = found;
    at = found + 1;
  }
  return score;
}

/** rank keeps the original order for an empty query, otherwise best first. */
export function rank<T>(query: string, items: T[], key: (item: T) => string): T[] {
  if (!query.trim()) return items;
  const scored: Array<{ item: T; at: number; score: number }> = [];
  items.forEach((item, at) => {
    const score = fuzzyScore(query, key(item));
    if (score !== null) scored.push({ item, at, score });
  });
  scored.sort((a, b) => b.score - a.score || a.at - b.at);
  return scored.map((entry) => entry.item);
}

export function buildCommands(ctx: PaletteContext, query: string): PaletteCommand[] {
  const { peers, routes, logs, connection, departed, navigate } = ctx;
  const commands: PaletteCommand[] = [];

  for (const { label, to } of destinations) {
    commands.push({ id: `nav-${to}`, group: "navigate", label, hint: to, run: () => navigate(to) });
  }

  commands.push({
    id: departed ? "action-rejoin" : connection === "paused" ? "action-connect" : "action-disconnect",
    group: "action",
    label: departed ? "Rejoin network" : connection === "paused" ? "Connect" : "Disconnect",
    hint: departed ? "Settings — needs a fresh invite" : connection === "paused" ? "resume the mesh" : "pause the mesh",
    run: departed
      ? () => navigate("/settings?network=rejoin")
      : connection === "paused"
        ? ctx.connect
        : ctx.disconnect,
  });
  commands.push({ id: "action-service-start", group: "action", label: "Start service", hint: "system service lifecycle", run: ctx.startService });
  commands.push({ id: "action-service-stop", group: "action", label: "Stop service", hint: "system service lifecycle", run: ctx.stopService });
  commands.push({ id: "action-service-restart", group: "action", label: "Restart service", hint: "system service lifecycle", run: ctx.restartService });
  commands.push({
    id: "action-invite",
    group: "action",
    label: "Generate invitation",
    hint: "Invitations — secrets are only shown there",
    run: () => navigate("/invites"),
  });
  commands.push({
    id: "action-rotate",
    group: "action",
    label: "Rotate network secret",
    hint: "Security — confirmed there",
    run: () => navigate("/security"),
  });
  commands.push({
    id: "action-reset",
    group: "action",
    label: "Reset network configuration",
    hint: "Settings — confirmed there",
    run: () => navigate("/settings?network=switch"),
  });

  for (const peer of peers) {
    const name = peer.name || shortID(peer.id);
    commands.push({
      id: `peer-${peer.id}`,
      group: "peer",
      label: `Open ${name}`,
      hint: [peer.overlay_ipv4, peer.state, peer.direct ? "direct" : "no direct link"].filter(Boolean).join(" · "),
      run: () => navigate(`/peers?peer=${encodeURIComponent(peer.id)}`),
    });
    commands.push({
      id: `probe-${peer.id}`,
      group: "action",
      label: `Probe ${name}`,
      hint: "ask the daemon for a fresh probe",
      run: () => ctx.probe(peer.id),
    });
    // The page disables Ping without an overlay address; a palette entry that
    // cannot run is worse than no entry.
    if (peer.overlay_ipv4) {
      commands.push({
        id: `ping-${peer.id}`,
        group: "action",
        label: `Ping ${name}`,
        hint: `overlay ${peer.overlay_ipv4}`,
        run: () => ctx.ping(peer),
      });
    }
  }

  for (const route of routes) {
    commands.push({
      id: `route-${route.destination}`,
      group: "route",
      label: `Route ${shortID(route.destination)} via ${shortID(route.next_hop)}`,
      hint: `${route.hops} hops · ${route.cost_ms.toFixed(1)} ms`,
      run: () => navigate(`/network?peer=${encodeURIComponent(route.destination)}`),
    });
  }

  if (query.trim().length >= LogQueryMin) {
    for (const line of logs.slice(-100).reverse()) {
      commands.push({
        id: `log-${line.time_ms}-${line.level}-${line.message.slice(0, 12)}`,
        group: "log",
        label: line.message,
        hint: `${new Date(line.time_ms).toLocaleTimeString()} · ${line.level}`,
        // The Diagnostics page filters by substring, so jump with a prefix of
        // the line rather than the whole (possibly very long) message.
        run: () => navigate(`/logs?q=${encodeURIComponent(line.message.trim().slice(0, 24))}`),
      });
    }
  }

  const ranked = rank(query, commands, (command) => `${command.label} ${command.hint}`);
  const seen = new Map<CommandGroup, number>();
  const out: PaletteCommand[] = [];
  for (const command of ranked) {
    const max = command.group === "log" ? LogResultsMax : command.group === "route" ? RouteResultsMax : Number.POSITIVE_INFINITY;
    const used = seen.get(command.group) ?? 0;
    if (used >= max) continue;
    seen.set(command.group, used + 1);
    out.push(command);
  }
  return out;
}
