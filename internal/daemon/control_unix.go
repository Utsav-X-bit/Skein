//go:build !windows

package daemon

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// serveControl serves the desktop control API over the Unix socket. The
// caller identity comes from SO_PEERCRED; no bearer token is used.
func (d *Daemon) serveControl(ctx context.Context) {
	cfg := d.config()
	if cfg.API.Socket == "" {
		return
	}
	ln, err := api.NewUnixListener(cfg.API.Socket, "skein", "/etc/skein/access")
	if err != nil {
		log.Printf("skein: error: control socket: %v", err)
		return
	}
	srv := &http.Server{
		Handler:           d.api.Handler(),
		ConnContext:       api.CallerContext,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("skein: error: control socket: %v", err)
	}
}
