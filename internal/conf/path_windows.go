//go:build windows

package conf

// elevated is unknown on Windows; the daemon reports the real error.
func elevated() bool { return false }
