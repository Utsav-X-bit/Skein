import { beforeEach, describe, expect, it } from "vitest";
import { EventLimit, eventKey, useSessionEvents, type SessionEvent } from "./store";
import type { DaemonEvent } from "@/lib/api";

const event = (over: Partial<DaemonEvent> = {}): DaemonEvent => ({
  type: "peer_up",
  peer_id: "0123456789abcdef",
  time_ms: 1_000,
  ...over,
});

const push = (ev: DaemonEvent) => useSessionEvents.getState().push(ev);
const events = () => useSessionEvents.getState().events;

beforeEach(() => useSessionEvents.setState({ events: [] }));

describe("session event store", () => {
  it("keeps one occurrence per type|peer|time", () => {
    push(event());
    push(event({ detail: "replayed" }));
    expect(events()).toHaveLength(1);
    push(event({ time_ms: 2_000 }));
    expect(events()).toHaveLength(2);
    expect(events()[1].key).toBe(eventKey(event({ time_ms: 2_000 })));
  });

  it("keeps distinct peer and type separate", () => {
    push(event());
    push(event({ peer_id: "fedcba9876543210" }));
    push(event({ type: "peer_removed" }));
    expect(events()).toHaveLength(3);
  });

  it("is bounded and drops the oldest", () => {
    for (let i = 0; i < EventLimit + 5; i++) push(event({ time_ms: i }));
    const rows: SessionEvent[] = events();
    expect(rows).toHaveLength(EventLimit);
    expect(rows[0].time_ms).toBe(5);
    expect(rows[rows.length - 1].time_ms).toBe(EventLimit + 4);
  });
});
