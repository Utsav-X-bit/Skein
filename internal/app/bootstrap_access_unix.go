//go:build !windows

package app

// bootstrapAccessPath returns the access-file path for bootstrap
// mode on Unix, where AccessPath is a constant.
func bootstrapAccessPath() string { return AccessPath }
