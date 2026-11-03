package main

import "log"

// registerUpdater installs the GUI update hook.
//
// ponytail: W4 hook — W10 implements the per-format update path (DEB/RPM/Arch
// via the package manager, AppImage self-update, signed helper on Windows).
func registerUpdater(a *App) {
	log.Println("updater hook registered (stub)")
}
