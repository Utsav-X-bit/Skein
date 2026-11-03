//go:build windows

package app

// checkSystemOwnership: on Windows the DACL (SYSTEM/Administrators) already
// restricts the config; ownership is not checked.
func checkSystemOwnership(string) error { return nil }
