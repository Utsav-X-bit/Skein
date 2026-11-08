import { describe, expect, it, vi } from "vitest";
import type { LogEntry, Peer, Route } from "@/lib/api";
import { buildCommands, fuzzyScore, rank, type PaletteContext } from "./palette";

const peer = (over: Partial<Peer> = {}): Peer => ({
  id: "0123456789abcdef",
  name: "laptop",
  state: "up",
  rtt_ms: 4,
  overlay_ipv4: "10.44.0.7",
  dialer: false,
  direct: true,
  rx_bytes: 10,
  tx_bytes: 20,
  last_seen_ms: 1_000,
  ...over,
});

const route = (over: Partial<Route> = {}): Route => ({
  destination: "fedcba9876543210",
  next_hop: "0123456789abcdef",
  cost_ms: 12.5,
  hops: 2,
  path: ["0123456789abcdef", "fedcba9876543210"],
  ...over,
});

const log = (message: string, over: Partial<LogEntry> = {}): LogEntry => ({
  time_ms: 1_700_000_000_000,
  level: "info",
  message,
  ...over,
});

function context(over: Partial<PaletteContext> = {}) {
  const navigate = vi.fn();
  const ctx: PaletteContext = {
    peers: [],
    routes: [],
    logs: [],
    connection: "connected",
    departed: false,
    navigate,
    connect: vi.fn(),
    disconnect: vi.fn(),
    startService: vi.fn(),
    stopService: vi.fn(),
    restartService: vi.fn(),
    probe: vi.fn(),
    ping: vi.fn(),
    ...over,
  };
  return { ctx, navigate };
}

describe("palette matching", () => {
  it("ranks a contiguous substring above scattered characters", () => {
    const text = "Rotate network secret";
    expect(fuzzyScore("rotate", text)).toBeGreaterThan(fuzzyScore("rte", text) ?? 0);
    expect(fuzzyScore("zzz", text)).toBeNull();
  });

  it("keeps input order for an empty query and drops non-matches", () => {
    expect(rank("", ["b", "a"], (v) => v)).toEqual(["b", "a"]);
    expect(rank("in", ["peers", "invites", "settings"], (v) => v)).toEqual(["invites", "settings"]);
    expect(rank("zzz", ["peers", "invites"], (v) => v)).toEqual([]);
  });
});

describe("palette commands", () => {
  it("lists navigation first when nothing is typed", () => {
    const { ctx } = context();
    const commands = buildCommands(ctx, "");
    expect(commands[0]).toMatchObject({ group: "navigate", label: "Overview", hint: "/" });
    expect(commands.some((c) => c.group === "log")).toBe(false);
  });

  it("routes destructive actions to the page that owns the confirmation", () => {
    const { ctx, navigate } = context();
    const commands = buildCommands(ctx, "");
    const rotate = commands.find((c) => c.id === "action-rotate");
    const reset = commands.find((c) => c.id === "action-reset");
    const invite = commands.find((c) => c.id === "action-invite");
    expect(rotate?.label).toBe("Rotate network secret");
    rotate?.run();
    expect(navigate).toHaveBeenLastCalledWith("/security");
    reset?.run();
    expect(navigate).toHaveBeenLastCalledWith("/settings?network=switch");
    invite?.run();
    expect(navigate).toHaveBeenLastCalledWith("/invites");
  });

  it("reflects the connection state with one connect-or-disconnect entry", () => {
    const connected = context();
    const disconnect = buildCommands(connected.ctx, "").find((c) => c.id === "action-disconnect");
    disconnect?.run();
    expect(connected.ctx.disconnect).toHaveBeenCalledOnce();
    expect(connected.ctx.connect).not.toHaveBeenCalled();

    const paused = context({ connection: "paused" });
    buildCommands(paused.ctx, "").find((c) => c.id === "action-connect")?.run();
    expect(paused.ctx.connect).toHaveBeenCalledOnce();

    const departed = context({ departed: true });
    const rejoin = buildCommands(departed.ctx, "").find((c) => c.id === "action-rejoin");
    rejoin?.run();
    expect(departed.navigate).toHaveBeenCalledWith("/settings?network=rejoin");
  });

  it("searches peers by name and runs the same probe and ping as the page", () => {
    const laptop = peer();
    const { ctx, navigate } = context({ peers: [laptop] });
    const commands = buildCommands(ctx, "laptop");
    expect(commands[0]).toMatchObject({ group: "peer", label: "Open laptop" });
    commands[0].run();
    expect(navigate).toHaveBeenCalledWith(`/peers?peer=${encodeURIComponent(laptop.id)}`);
    commands.find((c) => c.id === `probe-${laptop.id}`)?.run();
    expect(ctx.probe).toHaveBeenCalledWith(laptop.id);
    commands.find((c) => c.id === `ping-${laptop.id}`)?.run();
    expect(ctx.ping).toHaveBeenCalledWith(laptop);
  });

  it("omits ping for a peer without an overlay address", () => {
    const { ctx } = context({ peers: [peer({ overlay_ipv4: undefined })] });
    expect(buildCommands(ctx, "laptop").some((c) => c.id.startsWith("ping-"))).toBe(false);
  });

  it("finds routes and jumps to the destination peer", () => {
    const { ctx, navigate } = context({ routes: [route()] });
    const commands = buildCommands(ctx, "route");
    const hit = commands.find((c) => c.group === "route");
    expect(hit?.hint).toBe("2 hops · 12.5 ms");
    hit?.run();
    expect(navigate).toHaveBeenCalledWith("/network?peer=fedcba9876543210");
  });

  it("offers bounded log results only once the query can narrow them", () => {
    const logs = Array.from({ length: 20 }, (_, i) => log(`connection reset ${i}`));
    const { ctx, navigate } = context({ logs });
    expect(buildCommands(ctx, "c").some((c) => c.group === "log")).toBe(false);
    const hits = buildCommands(ctx, "connection reset").filter((c) => c.group === "log");
    expect(hits.length).toBe(5);
    hits[0].run();
    expect(navigate).toHaveBeenCalledWith(`/logs?q=${encodeURIComponent("connection reset 19")}`);
  });

  it("calls the service bindings the settings page calls", () => {
    const { ctx } = context();
    const commands = buildCommands(ctx, "");
    commands.find((c) => c.id === "action-service-start")?.run();
    commands.find((c) => c.id === "action-service-stop")?.run();
    commands.find((c) => c.id === "action-service-restart")?.run();
    expect(ctx.startService).toHaveBeenCalledOnce();
    expect(ctx.stopService).toHaveBeenCalledOnce();
    expect(ctx.restartService).toHaveBeenCalledOnce();
  });
});
