import { describe, expect, it } from "vitest";
import { connectLabel, connectMode } from "./connectState";

describe("connect mode", () => {
  it("offers Disconnect whenever the daemon reports connected", () => {
    // Regression: the button also required a cached `vpn_active` flag,
    // which was read once at mount and never refreshed after the first
    // connect. Home then said "Connect" while the mesh was up, and the
    // only way to disconnect was Settings → Service.
    expect(connectMode("connected")).toBe("disconnect");
  });

  it("resumes a paused dataplane and reconnects a failed one", () => {
    expect(connectMode("paused")).toBe("resume");
    expect(connectMode("error")).toBe("reconnect");
  });

  it("connects when the mesh is down or its state is unknown", () => {
    expect(connectMode("disconnected")).toBe("connect");
    expect(connectMode("unknown")).toBe("connect");
    expect(connectMode("")).toBe("connect");
  });

  it("labels every mode", () => {
    expect(connectLabel.disconnect).toBe("Disconnect");
    expect(connectLabel.resume).toBe("Resume");
    expect(connectLabel.reconnect).toBe("Reconnect");
    expect(connectLabel.connect).toBe("Connect");
  });
});
