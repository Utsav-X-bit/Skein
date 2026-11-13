/**
 * connectMode is the Home primary button's whole state machine (design
 * brief §3.1, §5.2) as a pure function.
 *
 * The daemon's connection state is the only input. A connected mesh
 * means the VPN service is up by definition — the daemon reporting
 * "connected" *is* proof that the in-process engine runs — so a
 * separately-cached `vpn_active` flag must never be able to turn
 * Disconnect back into Connect. That flag is read once at mount and
 * refreshed on permission events, so gating on it left Home offering
 * "Connect" (and no way to disconnect) right after the first connect.
 */
export type ConnectMode = "connect" | "disconnect" | "resume" | "reconnect";

export function connectMode(connectionState: string): ConnectMode {
  switch (connectionState) {
    case "connected":
      return "disconnect";
    case "paused":
      return "resume";
    // The daemon answered with an error, so it is running and the
    // dataplane is what failed: retry the connection, not the service.
    case "error":
      return "reconnect";
    default:
      return "connect";
  }
}

export const connectLabel: Record<ConnectMode, string> = {
  connect: "Connect",
  disconnect: "Disconnect",
  resume: "Resume",
  reconnect: "Reconnect",
};
