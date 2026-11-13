import { describe, expect, it } from "vitest";
import { MTU_MAX, isValidMtu, isValidPort } from "./limits";

describe("config bounds", () => {
  it("caps MTU at the wire frame, not the port range", () => {
    // conf.MaxMTU = MaxPayload(1400−26) − nonce 12 − tag 16 = 1346.
    // The forms used to allow 65535, so any MTU above 1346 was accepted
    // by the UI and rejected by the daemon.
    expect(MTU_MAX).toBe(1346);
    expect(isValidMtu("1280")).toBe(true);
    expect(isValidMtu("1346")).toBe(true);
    expect(isValidMtu("1347")).toBe(false);
    expect(isValidMtu("65535")).toBe(false);
    expect(isValidMtu("575")).toBe(false);
    expect(isValidMtu("")).toBe(false);
    expect(isValidMtu("abc")).toBe(false);
  });

  it("validates the listen port range", () => {
    expect(isValidPort("11010")).toBe(true);
    expect(isValidPort("1")).toBe(true);
    expect(isValidPort("65535")).toBe(true);
    expect(isValidPort("0")).toBe(false);
    expect(isValidPort("65536")).toBe(false);
    expect(isValidPort("11010.5")).toBe(false);
  });
});
