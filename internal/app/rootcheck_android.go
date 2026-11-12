//go:build android

package app

// checkSystemOwnership is a no-op on Android: there is no root-owned
// system config — the daemon runs in-process as the app user and its
// config lives in the app-private files dir, which only the app's UID
// can write.
func checkSystemOwnership(configPath string) error { return nil }
