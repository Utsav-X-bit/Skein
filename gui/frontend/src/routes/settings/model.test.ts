import { describe, expect, it } from "vitest";
import type { ConfigView } from "@/lib/api";
import { MTU_IPV6_MIN, draftFrom, mtuIPv6Message, toPatch, type NodeDraft } from "./model";

const config = (over: Partial<ConfigView> = {}): ConfigView => ({
  name: "studio",
  overlay_ipv4_prefix: "10.42.0.0/16",
  overlay_ipv6_prefix: "fd00:6d65:7368::/64",
  overlay_ipv4: "10.42.0.2",
  overlay_ipv6: "fd00:6d65:7368::2",
  listen_port: 51820,
  mtu: 1280,
  interface: "skein0",
  api_listen: "",
  api_socket: "/run/skein.sock",
  mdns: true,
  stun: true,
  stun_server: "",
  protocol: 2,
  peers: null,
  network_secret: "redacted",
  api_token: "redacted",
  ...over,
});

const draft = (over: Partial<NodeDraft> = {}): NodeDraft => ({ ...draftFrom(config()), ...over });

const patchOf = (d: NodeDraft, c = config()) => {
  const result = toPatch(d, c);
  if ("error" in result) throw new Error(result.error);
  return result.patch;
};

describe("settings patch", () => {
  it("sends only the name for a rename, so the daemon reports no restart", () => {
    const patch = patchOf(draft({ name: "kitchen" }));
    expect(patch).toEqual({ name: "kitchen" });
  });

  it("sends nothing when the draft matches the authoritative config", () => {
    expect(patchOf(draft())).toEqual({});
    // Re-typed but numerically identical values are not a change.
    expect(patchOf(draft({ port: "051820", mtu: "01280" }))).toEqual({});
  });

  it("sends each restart-dependent field only when it really changed", () => {
    expect(patchOf(draft({ port: "51821" }))).toEqual({ listen_port: 51821 });
    expect(patchOf(draft({ mtu: "1346" }))).toEqual({ mtu: 1346 });
    expect(patchOf(draft({ mdns: false }))).toEqual({ mdns: false });
  });

  it("compares the trimmed name the daemon will store", () => {
    expect(patchOf(draft({ name: "  studio  " }))).toEqual({});
    expect(patchOf(draft({ name: " studio 2 " }))).toEqual({ name: "studio 2" });
  });

  it("rejects an MTU the IPv6 overlay cannot carry before the round trip", () => {
    const result = toPatch(draft({ mtu: String(MTU_IPV6_MIN - 1) }), config());
    expect(result).toEqual({ error: mtuIPv6Message });
    // A v4-only node may go lower; the daemon accepts it there.
    expect(patchOf(draft({ mtu: "1200" }), config({ overlay_ipv6: "" }))).toEqual({ mtu: 1200 });
  });

  it("never blocks an unchanged low MTU on a v4-only node", () => {
    const v4Only = config({ overlay_ipv6: "", mtu: 1200 });
    expect(patchOf({ ...draftFrom(v4Only), name: "kitchen" }, v4Only)).toEqual({ name: "kitchen" });
  });

  it("keeps the range errors for values the daemon would reject", () => {
    expect(toPatch(draft({ port: "0" }), config())).toHaveProperty("error");
    expect(toPatch(draft({ mtu: "2000" }), config())).toHaveProperty("error");
  });

});

