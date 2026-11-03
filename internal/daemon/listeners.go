package daemon

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

// Service defaults for the control transport: the socket is 0660 root:skein,
// and per-user grants live in /etc/skein/access (written by the helper).
const (
	controlSocketGroup = "skein"
	controlAccessFile  = "/etc/skein/access"
)

// StartControlListeners serves the API on the configured service control
// transport: the Unix socket on unix, the named pipe on Windows (see
// controlListener). Bind failure is returned for the caller to treat as
// fatal; the dev-only TCP listener (serveAPI) stays non-fatal and is
// untouched. The returned stop function shuts the listener down and removes
// the socket file.
func (d *Daemon) StartControlListeners(ctx context.Context) (func(), error) {
	ln, label, remove, err := d.controlListener(ctx)
	if err != nil {
		return nil, err
	}
	if ln == nil {
		return func() {}, nil
	}

	srv := controlServer(ctx, d.api.Handler())
	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		if remove != "" {
			_ = os.Remove(remove)
		}
	}
	go func() {
		<-ctx.Done()
		stop()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("skein: error: %s: %v", label, err)
		}
	}()
	return stop, nil
}

// controlServer builds the HTTP server for a control transport.
func controlServer(ctx context.Context, handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}
