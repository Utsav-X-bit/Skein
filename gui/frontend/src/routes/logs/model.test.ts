import { describe, expect, it } from "vitest";
import type { LogEntry } from "@/lib/api";
import { filterLogs, logKey, logLine, mergeLogs } from "./model";

const entry = (message: string, over: Partial<LogEntry> = {}): LogEntry => ({
  time_ms: 1_700_000_000_000,
  level: "info",
  message,
  ...over,
});

describe("log tail merge", () => {
  it("keeps one copy of a line that arrived both in the page and over SSE", () => {
    const line = entry("session up");
    const merged = mergeLogs([line], [line]);
    expect(merged).toHaveLength(1);
    expect(logKey(merged[0])).toBe(logKey(line));
  });

  it("orders by time and keeps the newest entries when capped", () => {
    const merged = mergeLogs(
      [entry("old", { time_ms: 1 }), entry("middle", { time_ms: 3 })],
      [entry("new", { time_ms: 2 })],
      2,
    );
    expect(merged.map((row) => row.message)).toEqual(["new", "middle"]);
  });

  it("treats a missing page as empty rather than failing", () => {
    expect(mergeLogs(null, [entry("live")]).map((row) => row.message)).toEqual(["live"]);
    expect(mergeLogs(undefined, [])).toEqual([]);
  });
});

describe("log filters", () => {
  const rows = [entry("peer up", { level: "info" }), entry("dial failed", { level: "error" }), entry("slow handshake", { level: "warning" })];

  it("filters by the daemon's own level", () => {
    expect(filterLogs(rows, "error", "").map((row) => row.message)).toEqual(["dial failed"]);
    expect(filterLogs(rows, "warning", "").map((row) => row.message)).toEqual(["slow handshake"]);
  });

  it("matches the message and the level case-insensitively", () => {
    expect(filterLogs(rows, "all", "PEER").map((row) => row.message)).toEqual(["peer up"]);
    expect(filterLogs(rows, "all", "error").map((row) => row.message)).toEqual(["dial failed"]);
    expect(filterLogs(rows, "all", "nothing")).toEqual([]);
  });

  it("copies the timestamp, level and message", () => {
    expect(logLine(entry("ready"))).toBe("2023-11-14T22:13:20.000Z INFO ready");
  });
});
