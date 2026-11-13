/**
 * Which screen owns the window: the setup flow or the shell.
 *
 * The bootstrap helper registers and starts the service *before* the GUI
 * posts its token, so the service query stops reporting "missing" while the
 * flow is still running. A gate that followed that live state unmounted the
 * onboarding screen — and the once-only join string it keeps in component
 * state — mid-setup, which read as an unconfigured, "offline" app until the
 * CLI was run by hand.
 *
 * "auto" follows the OS, so a service installed outside the flow (installer,
 * CLI, another window) takes over while the user has not begun. "open" is a
 * flow the user has started: it owns the window until it exits, through both
 * the pending and the result screen. "closed" is that exit, and it re-arms to
 * "auto" once the service is installed, so a later uninstall shows setup
 * again instead of an empty shell.
 */
export type SetupGate = "auto" | "open" | "closed";

export function nextSetupGate(gate: SetupGate, serviceMissing: boolean): SetupGate {
  if (gate === "closed" && !serviceMissing) return "auto";
  return gate;
}

/**
 * Settings is never covered mid-setup: recovery must stay reachable. A device
 * with no configuration at all is the exception — there is nothing left on
 * Settings to recover, and a reset that leaves the user there had no way
 * forward, so setup owns the window even on /settings.
 */
export function setupOwnsWindow(
  gate: SetupGate,
  serviceMissing: boolean,
  pathname: string,
  configMissing = false,
): boolean {
  if (configMissing) return true;
  if (pathname.startsWith("/settings")) return false;
  return gate === "open" || (gate === "auto" && serviceMissing);
}
