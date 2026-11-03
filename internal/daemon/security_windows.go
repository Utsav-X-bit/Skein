//go:build windows

package daemon

// fileOwner reports nothing on Windows: ownership is DACL-based, and the
// security card surfaces that through the manager fields instead.
func fileOwner(string) string { return "" }
