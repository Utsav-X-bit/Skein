//go:build !android

package daemon

// androidStateFile is empty off Android: only the Android daemon
// keeps its paused state in the app-private files dir.
func androidStateFile() string { return "" }
