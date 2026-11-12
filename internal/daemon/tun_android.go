//go:build android

package daemon

import (
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/tun"
)

// openTun opens the Android TUN device. The TUN already exists:
// SkeinVpnService built it and handed its FD over via
// tun.SetAndroidFd. Wait for it (bounded) rather than opening
// /dev/net/tun, which an unprivileged app cannot do.
func openTun(cfg conf.Config) (tun.Device, error) {
	return tun.OpenAndroid(cfg.Node.MTU)
}
