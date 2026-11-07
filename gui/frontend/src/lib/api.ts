import * as AppService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/appservice";
import * as ConnectionService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/connectionservice";
import * as PeerService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/peerservice";
import * as NetworkService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/networkservice";
import * as ConfigService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/configservice";
import * as InviteService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/inviteservice";
import * as DiagnosticsService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/diagnosticsservice";
import * as FirewallService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/firewallservice";
import * as ServiceService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/serviceservice";
import * as HistoryService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/historyservice";
import * as UpdateService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/updateservice";

export { AppService, ConnectionService, PeerService, NetworkService, ConfigService, InviteService, DiagnosticsService, FirewallService, ServiceService, HistoryService, UpdateService };

export type { Connection, Status, Peer, Route, Activity, ConfigView, ConfigPatch, ConfigPatchResult, Invite, LogEntry, FirewallStatus, SecurityStatus, ServiceStatus, ProbeResult, DiagnosticBundle, Capabilities } from "../../bindings/github.com/Utsav-X-bit/skein/internal/api/models";

// DaemonEvent is the lifecycle envelope from internal/api.Event. The Go
// bridge (gui/services/events.go) forwards each one as a Wails event named
// after its type, so the payload arrives as this object. Lifecycle events
// describe what happened; they are not telemetry.
export type DaemonEvent = {
  id?: number;
  type: string;
  peer_id?: string;
  detail?: string;
  time_ms: number;
};

// lifecycleEvents mirrors the forwarding allowlist in gui/services/events.go.
export const lifecycleEvents = [
  "peer_up",
  "peer_joined",
  "peer_discovered",
  "peer_added",
  "peer_removed",
  "peer_down",
  "peer_departed",
  "dialer_fallback",
  "peer:leave",
  "peer:rejoined",
  "name_changed",
  "secret_rotated",
  "system_reset",
  "system_imported",
  "leave_dataplane_stop_failed",
] as const;

export type LifecycleEvent = (typeof lifecycleEvents)[number];

export function errText(e: unknown): string {
  if (!e) return "Unknown error";
  if (typeof e === "string") return e;
  if (typeof e === "object" && "message" in e) return String((e as { message: unknown }).message);
  return String(e);
}

export function isOffline(e: unknown): boolean {
  const s = errText(e).toLowerCase();
  return s.includes("daemon not connected") || s.includes("no such file") || s.includes("connection refused") || s.includes("connect:");
}

// elevationCancelled recognises a dismissed or failed privilege prompt.
// pkexec exits 126 when the user dismisses it, 127 when authentication
// fails, and 125 on a polkit error; the Windows helper reports that UAC
// "may have been cancelled". A raw exit code is not user guidance.
export function elevationCancelled(e: unknown): boolean {
  const s = errText(e).toLowerCase();
  return /exit status 12[567]/.test(s) || s.includes("may have been cancelled");
}

// errDisplay is what the user should read: the same text as errText,
// except a dismissed privilege prompt becomes actionable guidance
// instead of a raw helper exit code.
export function errDisplay(e: unknown): string {
  if (elevationCancelled(e)) return "Administrator approval was not completed. Retry and approve only the Skein helper action.";
  return errText(e);
}

// errKind is the frontend's best-effort mapping of the Step 8
// three-state classification onto the error strings the Wails
// bindings deliver (the Go/CLI classification in internal/ctlerr is
// authoritative; the frontend only has the rendered string). It
// returns "" when the kind cannot be told, so callers never invent
// a remediation from an unrecognised message.
export function errKind(e: unknown): "" | "Daemon offline" | "Access denied" | "Not recognised" | "Config locked" {
  const s = errText(e).toLowerCase();
  if (s.includes("daemon not connected") || s.includes("daemon offline") || s.includes("connection refused") || s.includes("no such file")) return "Daemon offline";
  if (s.includes("access denied") || s.includes("forbidden") || s.includes("role required")) return "Access denied";
  if (s.includes("unauthenticated") || s.includes("unauthorized") || s.includes("did not recognise")) return "Not recognised";
  if (s.includes("config locked") || s.includes("423")) return "Config locked";
  return "";
}
