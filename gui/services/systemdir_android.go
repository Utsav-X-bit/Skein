//go:build android

package services

// liveSystemDir is the privileged install root the GUI must never
// write. Empty on Android: the app-private files dir (app.SystemDir)
// is owned by the app itself — the GUI and the daemon are the same
// process there — so it is not a protected system directory, and
// onboarding must be allowed to create the identity in it.
func liveSystemDir() string { return "" }
