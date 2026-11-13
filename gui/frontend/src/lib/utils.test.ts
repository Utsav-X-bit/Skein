import { describe, expect, it } from "vitest";
import { stripBindingNoise } from "./utils";

describe("stripBindingNoise", () => {
  it("removes the Wails transport wrappers and keeps the backend message", () => {
    // Exactly what the phone showed for a failed Connect.
    expect(
      stripBindingNoise(
        "Binding call failed: Bound method returned an error: The helper reported success, but the service is stopped instead of running. Open Diagnostics → Logs, then retry.",
      ),
    ).toBe("The helper reported success, but the service is stopped instead of running. Open Diagnostics → Logs, then retry.");
  });

  it("leaves an unwrapped message alone", () => {
    expect(stripBindingNoise("The VPN bridge is not ready yet")).toBe("The VPN bridge is not ready yet");
    // A message that merely mentions a binding is not a wrapper.
    expect(stripBindingNoise("binding call refused by policy")).toBe("binding call refused by policy");
  });

  it("handles a single wrapper and surrounding whitespace", () => {
    expect(stripBindingNoise("  Binding call failed: daemon offline  ")).toBe("daemon offline");
    expect(stripBindingNoise("")).toBe("");
  });
});
