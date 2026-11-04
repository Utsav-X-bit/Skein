//go:build windows

package app

// bootstrapAccessPath returns the access-file path for bootstrap
// mode on Windows, where AccessPath is a function of ProgramData.
func bootstrapAccessPath() string { return AccessPath() }
