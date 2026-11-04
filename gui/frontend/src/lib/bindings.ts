// Binding-availability guards (M7).
//
// Wails v3 bindings are generated at build time and each Go method is
// reached through an opaque Call.ByID number. A Go method added after
// the last Wails build therefore has NO callable frontend function:
// the checked-in binding file simply does not export it, and calling
// it would throw at runtime ("... is not a function").
// The checked-in bindings predate M5 Steps 7-8, so these methods exist
// in Go but are not yet bound:
//   PeerService.LeaveNetwork, UpdateService.CheckManifest / Apply /
//   ReloadGrants, ServiceService.InstallWithDaemon / UninstallWithPurge,
//   SetupService.ResetWithPurge.
// Until a Wails build on a GUI machine regenerates bindings, screens
// must NOT call them. They check these guards first and render a
// disabled state with the exact reason instead of a crashing button.
// After regeneration the same code paths activate with no further
// frontend change. Never hand-edit the generated binding files to
// invent a ByID number: a wrong ID calls the wrong Go method.
import * as PeerService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/peerservice";
import * as UpdateService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/updateservice";
import * as ServiceService from "../../bindings/github.com/Utsav-X-bit/skein/gui/services/serviceservice";

function has(obj: object, name: string): boolean {
  return typeof (obj as Record<string, unknown>)[name] === "function";
}

export const hasLeaveBinding = () => has(PeerService, "LeaveNetwork");
export const hasUpdateCheckBinding = () => has(UpdateService, "CheckManifest");
export const hasUpdateApplyBinding = () => has(UpdateService, "Apply");
export const hasReloadGrantsBinding = () => has(UpdateService, "ReloadGrants");
export const hasInstallWithDaemonBinding = () => has(ServiceService, "InstallWithDaemon");
export const hasUninstallWithPurgeBinding = () => has(ServiceService, "UninstallWithPurge");

// Result shapes, matching the Go JSON tags in internal/api/types_system.go.
// These types are NOT in the checked-in binding models (those also predate
// the methods), so they are declared here from the Go source.
export interface LeaveResult {
  departed: boolean;
  propagated: boolean;
  peers_left: number;
  routes_withdrawn: number;
  left_at_ms: number;
}

export interface UpdateState {
  version?: string;
  applied: boolean;
  rolled_back: boolean;
  error?: string;
  backup?: string;
  at_ms: number;
}

export interface UpdateCheckResult {
  version: string;
  current_version: string;
  checked: boolean;
  update_available: boolean;
  last_update?: UpdateState | null;
}

export interface UpdateApplyResult {
  version: string;
  checked: boolean;
  detached: boolean;
  staged?: string;
}

// Typed callers for the post-regeneration bindings. Each throws a clear
// error (instead of a TypeError) if called before regeneration, so a
// future caller that forgets the guard still fails legibly.
export function callLeaveNetwork(): Promise<LeaveResult> {
  const fn = (PeerService as unknown as Record<string, unknown>).LeaveNetwork;
  if (typeof fn !== "function") throw new Error("LeaveNetwork binding not generated yet: run a Wails build on a GUI machine to regenerate bindings, then this button activates.");
  return (fn as () => Promise<LeaveResult>)();
}

export function callCheckManifest(manifestPath: string): Promise<UpdateCheckResult> {
  const fn = (UpdateService as unknown as Record<string, unknown>).CheckManifest;
  if (typeof fn !== "function") throw new Error("CheckManifest binding not generated yet: run a Wails build on a GUI machine to regenerate bindings, then update checks activate.");
  return (fn as (p: string) => Promise<UpdateCheckResult>)(manifestPath);
}

export function callApplyUpdate(manifestPath: string, stagedBinaryPath: string): Promise<UpdateApplyResult> {
  const fn = (UpdateService as unknown as Record<string, unknown>).Apply;
  if (typeof fn !== "function") throw new Error("Apply binding not generated yet: run a Wails build on a GUI machine to regenerate bindings, then update apply activates.");
  return (fn as (m: string, s: string) => Promise<UpdateApplyResult>)(manifestPath, stagedBinaryPath);
}

export function callReloadGrants(): Promise<void> {
  const fn = (UpdateService as unknown as Record<string, unknown>).ReloadGrants;
  if (typeof fn !== "function") throw new Error("ReloadGrants binding not generated yet: run a Wails build on a GUI machine to regenerate bindings.");
  return (fn as () => Promise<void>)();
}
