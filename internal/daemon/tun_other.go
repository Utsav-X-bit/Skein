//go:build !android

package daemon

import (
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/tun"
)

// openTun opens the platform TUN device (Linux /dev/net/tun via
// wgtun, or the Windows equivalent).
func openTun(cfg conf.Config) (tun.Device, error) {
	return tun.Open(cfg.Node.Interface, cfg.Node.MTU)
}
