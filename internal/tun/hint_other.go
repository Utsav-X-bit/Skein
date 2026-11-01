//go:build !windows

package tun

// hint is a no-op outside Windows.
func hint(err error) error { return err }
