import { describe, expect, it } from "vitest";
import { lifecycleEvents } from "@/lib/api";
import { peerLifecycle } from "./useDaemonEvents";

describe("lifecycle event forwarding", () => {
  it("forwards peer-down, departure and dialer fallback", () => {
    for (const name of ["peer_down", "peer_departed", "dialer_fallback"]) {
      expect(lifecycleEvents).toContain(name);
    }
  });

  it("refreshes peers and status on the two peer transitions", () => {
    expect(peerLifecycle.peer_down).toBe(true);
    expect(peerLifecycle.peer_departed).toBe(true);
    expect(peerLifecycle.dialer_fallback).toBeUndefined();
  });
});
