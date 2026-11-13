import type { ConfigPatch, ConfigView } from "@/lib/api";
import { isValidMtu, isValidPort, mtuRangeMessage, portRangeMessage } from "@/lib/limits";

export type NodeDraft = { name: string; port: string; mtu: string; mdns: boolean };

// conf.Validate rejects an MTU below 1280 while the node has an IPv6 overlay
// (internal/conf/config.go: "node.mtu ... is too small for an IPv6 overlay").
// A lower MTU is only valid for a v4-only node, and this form cannot remove
// the overlay, so the bound follows the config it is editing.
export const MTU_IPV6_MIN = 1280;
export const mtuIPv6Message = `MTU must be at least ${MTU_IPV6_MIN} for this node's IPv6 overlay.`;

export const draftFrom = (c: ConfigView): NodeDraft => ({
  name: c.name ?? "",
  port: String(c.listen_port),
  mtu: String(c.mtu),
  mdns: c.mdns,
});

/**
 * toPatch builds the smallest patch that expresses the draft. The daemon
 * marks listen_port, mtu and mdns as restart-required on any presence, so
 * echoing untouched fields made a rename demand a restart.
 */
export function toPatch(draft: NodeDraft, current: ConfigView): { patch: ConfigPatch } | { error: string } {
  if (!isValidPort(draft.port)) {
    return { error: portRangeMessage };
  }
  if (!isValidMtu(draft.mtu)) {
    return { error: mtuRangeMessage };
  }
  const port = Number(draft.port);
  const mtu = Number(draft.mtu);
  const mtuChanged = mtu !== current.mtu;
  // ponytail: overlay_ipv6 is the live view; a host with IPv6 disabled hides
  // it while the daemon still validates the config's address, so that rarer
  // case falls back to the daemon's own field-scoped error.
  if (mtuChanged && current.overlay_ipv6 && mtu < MTU_IPV6_MIN) {
    return { error: mtuIPv6Message };
  }

  const name = draft.name.trim();
  const patch: ConfigPatch = {};
  if (name !== (current.name ?? "").trim()) patch.name = name;
  if (port !== current.listen_port) patch.listen_port = port;
  if (mtuChanged) patch.mtu = mtu;
  if (draft.mdns !== current.mdns) patch.mdns = draft.mdns;
  return { patch };
}

