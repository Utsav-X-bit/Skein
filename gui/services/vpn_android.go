//go:build android

package services

import (
	"github.com/Utsav-X-bit/skein/internal/android"
)

// vpnActive reports whether the in-process daemon is running.
func vpnActive() bool { return android.Started() }
