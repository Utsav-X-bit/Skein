//go:build android

package daemon

import (
	"context"
	"net"
	"os"
	"strconv"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/inproc"
)

// controlAccessFile is the access file for the Android app-private
// control plane. It lives in the app's private files dir (set by
// SetAndroidFilesDir before daemon.New runs; the init default is the
// standard files dir for this applicationId). A missing file leaves
// the grant table unconfigured, as on desktop.
var controlAccessFile string

// controlListener serves the control API in-process: on Android the
// GUI and the daemon are the same process and the same UID, and no
// Unix socket is exposed to other processes, so the control plane is
// the in-process listener from internal/inproc instead of a socket in
// /run/skein. StartControlListeners serves it with the same
// http.Server (and the same ConnContext) as the socket, so the API,
// its role gate, and its audit hook behave identically.
func (d *Daemon) controlListener(_ context.Context) (net.Listener, string, string, error) {
	// The in-process caller is the app itself (same UID, same
	// process). Grant it the secret role so the GUI can use every
	// endpoint the desktop GUI uses over the socket — including
	// network-invite generation and secret rotation — while wrapRole
	// still enforces the per-route role gate. No other process can
	// reach this listener: it is not backed by a socket, pipe, or
	// TCP port, so the grant is not reachable from outside the app.
	uid := os.Getuid()
	d.api.SetGrant("uid:"+strconv.Itoa(uid), api.RoleSecret)
	return inproc.Current(), "in-process control", "", nil
}
