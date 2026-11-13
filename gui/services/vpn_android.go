//go:build android

package services

import (
	"github.com/Utsav-X-bit/skein/internal/android"
	"github.com/Utsav-X-bit/skein/internal/daemon"
)

// vpnActive reports whether the tunnel is actually up: the in-process
// daemon is running AND its dataplane settled in "connected". A daemon
// that is merely running (paused or errored) is not a working VPN, and
// reporting it as active made the UI offer Disconnect for a tunnel that
// carried no traffic.
func vpnActive() bool {
	if !android.Started() {
		return false
	}
	return android.Connection().State == daemon.ConnConnected
}
