//go:build unix && !android

package daemon

import (
	"context"
	"fmt"
	"net"

	"github.com/Utsav-X-bit/skein/internal/api"
)

const controlAccessFile = "/etc/skein/access"

// controlListener opens the Unix control socket at the configured path.
// Binding fails if a live daemon already owns the socket or the mode/ACL
// cannot be applied.
func (d *Daemon) controlListener(_ context.Context) (net.Listener, string, string, error) {
	cfg := d.config()
	if cfg.API.Socket == "" {
		return nil, "", "", nil
	}
	ln, err := api.NewUnixListener(cfg.API.Socket, controlSocketGroup, controlAccessFile)
	if err != nil {
		return nil, "", "", fmt.Errorf("unix control socket %s: %w", cfg.API.Socket, err)
	}
	return ln, "unix socket " + cfg.API.Socket, cfg.API.Socket, nil
}
