import { describe, expect, it } from "vitest";
import { emptyRecorderState, stepRecorder, type RecorderInput } from "./useHistoryRecorder";

const Interval = 5_000;

const input = (over: Partial<RecorderInput> = {}): RecorderInput => ({
  enabled: true,
  hidden: false,
  node: "node-a",
  uptime: 100,
  t: 1_000,
  intervalMs: Interval,
  ...over,
});

describe("history recorder policy", () => {
  it("records nothing while disabled and forgets the baseline", () => {
    const step = stepRecorder({ node: "node-a", uptime: 100, t: 1_000, gap: false }, input({ enabled: false, t: 2_000 }));
    expect(step.record).toBe(false);
    expect(step.state).toEqual(emptyRecorderState);
  });

  it("drops the first sample and then records at the interval", () => {
    const first = stepRecorder(emptyRecorderState, input());
    expect(first.record).toBe(false);

    const tooSoon = stepRecorder(first.state, input({ t: 1_000 + Interval - 1 }));
    expect(tooSoon.record).toBe(false);

    const due = stepRecorder(first.state, input({ t: 1_000 + Interval }));
    expect(due.record).toBe(true);
    expect(due.state.t).toBe(1_000 + Interval);
  });

  it("drops the first sample after a hidden gap, then resumes", () => {
    const base = stepRecorder(emptyRecorderState, input()).state;
    const hidden = stepRecorder(base, input({ hidden: true, t: 1_000 + Interval }));
    expect(hidden.record).toBe(false);
    expect(hidden.state.gap).toBe(true);

    // First sample back has no proven window, even though the time is due.
    const afterGap = stepRecorder(hidden.state, input({ t: 1_000 + 60_000 }));
    expect(afterGap.record).toBe(false);
    expect(afterGap.state.gap).toBe(false);

    expect(stepRecorder(afterGap.state, input({ t: 1_000 + 65_000 })).record).toBe(true);
  });

  it("drops the sample that would bridge a node change", () => {
    const base = stepRecorder(emptyRecorderState, input()).state;
    const switched = stepRecorder(base, input({ node: "node-b", t: 1_000 + Interval }));
    expect(switched.record).toBe(false);
    expect(switched.state.node).toBe("node-b");

    expect(stepRecorder(switched.state, input({ node: "node-b", t: 1_000 + 2 * Interval })).record).toBe(true);
  });

  it("drops the sample that would bridge a restart (uptime decreased)", () => {
    const base = stepRecorder(emptyRecorderState, input()).state;
    const restarted = stepRecorder(base, input({ uptime: 5, t: 1_000 + Interval }));
    expect(restarted.record).toBe(false);
    expect(restarted.state.uptime).toBe(5);

    expect(stepRecorder(restarted.state, input({ uptime: 10, t: 1_000 + 2 * Interval })).record).toBe(true);
  });
});
