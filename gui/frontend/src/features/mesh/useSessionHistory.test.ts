import { beforeEach, describe, expect, it } from "vitest";
import { HistoryLimit, useSessionHistoryStore } from "./useSessionHistory";
import type { HistorySample } from "./model";

const sample = (t: number): HistorySample => ({
  t,
  rxPerSec: 100,
  txPerSec: 50,
  peersUp: 2,
  peersTotal: 3,
});

const record = (nodeId: string, uptime: number, t: number) =>
  useSessionHistoryStore.getState().record(nodeId, uptime, sample(t));
const samples = () => useSessionHistoryStore.getState().samples;

beforeEach(() => useSessionHistoryStore.getState().reset());

describe("session history ring", () => {
  it("appends and is bounded", () => {
    for (let i = 0; i < HistoryLimit + 5; i++) record("node-a", i, i);
    expect(samples()).toHaveLength(HistoryLimit);
    expect(samples()[0].t).toBe(5);
    expect(samples()[HistoryLimit - 1].t).toBe(HistoryLimit + 4);
  });

  it("keeps samples while the same node stays up", () => {
    record("node-a", 10, 1);
    record("node-a", 12, 2);
    expect(samples()).toHaveLength(2);
  });

  it("resets when uptime goes backwards", () => {
    record("node-a", 10, 1);
    record("node-a", 12, 2);
    record("node-a", 3, 3);
    expect(samples()).toHaveLength(1);
    expect(samples()[0].t).toBe(3);
    expect(useSessionHistoryStore.getState().uptime).toBe(3);
  });

  it("resets when the node id changes", () => {
    record("node-a", 10, 1);
    record("node-b", 11, 2);
    expect(samples()).toHaveLength(1);
    expect(useSessionHistoryStore.getState().nodeId).toBe("node-b");
  });
});
