import { describe, expect, it } from "vitest";
import { nextSetupGate, setupOwnsWindow } from "./onboardingGate";

describe("setup gate", () => {
  it("keeps setup mounted after the helper starts the service mid-flow", () => {
    // Regression: the helper registers and starts the service before the GUI
    // posts its token, so "missing" turns false while the flow is still
    // running. Following it unmounted the screen and lost the join string.
    expect(setupOwnsWindow("open", false, "/")).toBe(true);
    expect(setupOwnsWindow("open", false, "/peers")).toBe(true);
  });

  it("lets a service installed outside the flow take over before setup begins", () => {
    expect(setupOwnsWindow("auto", false, "/")).toBe(false);
  });

  it("shows setup on a fresh install", () => {
    expect(setupOwnsWindow("auto", true, "/")).toBe(true);
  });

  it("never covers Settings", () => {
    expect(setupOwnsWindow("open", true, "/settings")).toBe(false);
    expect(setupOwnsWindow("open", true, "/settings?network=switch")).toBe(false);
  });

  it("covers Settings when no configuration is left to recover", () => {
    // Regression: a completed network reset removes the config, but Settings
    // stayed uncovered, so the app sat on a dead mesh screen with no way back
    // to setup. Nothing on Settings can be repaired without a network.
    expect(setupOwnsWindow("auto", true, "/settings?network=switch", true)).toBe(true);
    expect(setupOwnsWindow("open", true, "/settings", true)).toBe(true);
    // A readable config keeps the exemption: recovery must stay reachable.
    expect(setupOwnsWindow("open", true, "/settings", false)).toBe(false);
  });

  it("keeps an explicit exit even while the cached service state is stale", () => {
    expect(setupOwnsWindow("closed", true, "/")).toBe(false);
  });

  it("re-arms a closed gate once the service is installed", () => {
    expect(nextSetupGate("closed", false)).toBe("auto");
    expect(nextSetupGate("closed", true)).toBe("closed");
    expect(nextSetupGate("open", false)).toBe("open");
    expect(nextSetupGate("auto", true)).toBe("auto");
  });
});
