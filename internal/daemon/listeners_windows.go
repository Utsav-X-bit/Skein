//go:build windows

package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/Utsav-X-bit/skein/internal/api"
)

var controlAccessFile = filepath.Join(os.Getenv("ProgramData"), "Skein", "access")

// controlListener opens the Windows named-pipe control server. Binding fails
// if a squatter already owns the pipe name or the DACL cannot be built.
func (d *Daemon) controlListener(ctx context.Context) (net.Listener, string, string, error) {
	cfg := d.config()
	if cfg.API.Pipe == "" {
		return nil, "", "", nil
	}
	ln, err := api.NewPipeListener(ctx, cfg.API.Pipe, controlAccessFile, d.api.ConnectionCap())
	if err != nil {
		return nil, "", "", fmt.Errorf("named pipe %s: %w", cfg.API.Pipe, err)
	}
	return ln, "named pipe \\\\.\\pipe\\" + cfg.API.Pipe, "", nil
}
