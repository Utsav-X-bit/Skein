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

export { AppService, ConnectionService, PeerService, NetworkService, ConfigService, InviteService, DiagnosticsService, FirewallService, ServiceService, HistoryService };

export type { Connection, Status, Peer, Route, Activity, ConfigView, ConfigPatch, ConfigPatchResult, Invite, LogEntry, FirewallStatus, SecurityStatus, ServiceStatus, ProbeResult, DiagnosticBundle, Capabilities } from "../../bindings/github.com/Utsav-X-bit/skein/internal/api/models";

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
