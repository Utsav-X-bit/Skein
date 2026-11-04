package main

import "log"

// registerUpdater installs the GUI update hook.
//
// M7: there is deliberately no automatic update check here, and this
// is not a stub waiting for a feed that does not exist. Skein updates
// are daemon self-updates driven by the operator: a signed manifest
// and binary are staged (release flow), the About screen verifies
// them with UpdateService.CheckManifest and launches the detached
// apply with UpdateService.Apply, then polls CheckManifest's
// LastUpdate for applied / rolled back. An automatic background
// check would need a version feed endpoint, which the daemon does
// not provide, and inventing one here would be a new update
// mechanism — exactly what the collapse plan forbids. This hook
// therefore only records the mechanism, so no code path can mistake
// a log line for a check.
func registerUpdater(a *App) {
	_ = a
	log.Println("updater: no automatic check; daemon updates are operator-driven via the About screen (CheckManifest/Apply over the daemon API)")
}
