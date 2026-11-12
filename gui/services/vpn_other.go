//go:build !android

package services

// vpnActive reports whether the in-process daemon is running.
// Off Android there is no in-process daemon, so it is always false.
func vpnActive() bool { return false }
