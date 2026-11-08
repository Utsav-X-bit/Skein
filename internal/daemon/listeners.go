package daemon

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// Service defaults for the control transport: the socket is 0660 root:skein,
// and per-user grants live in /etc/skein/access (written by the helper).
const controlSocketGroup = "skein"

// StartControlListeners is the single control listener (Step 2, ADR-001
// §5): it serves the API on the configured service control transport —
// the Unix socket on unix, the named pipe on Windows (see
// controlListener). It replaces the old serveControl paths, which are
// deleted; there is no second listener path left in this package.
// Bind failure is returned for the caller (Daemon.Run) to treat as
// fatal: a daemon whose control plane failed to bind must not keep
// running headless. The only non-fatal case is an unconfigured
// transport (no socket/pipe configured, dev mode): then it returns a
// no-op stop function and the caller logs loudly. The dev-only TCP
// listener (serveDevTCP) is gated out of the service path in Step 5
// (see devTCPEnabled): a config with a kernel transport never also
// serves TCP.
func (d *Daemon) StartControlListeners(ctx context.Context) (func(), error) {
	// Bootstrap mode already bound the control transport. Adopt it
	// rather than closing and rebinding: the listener never goes away,
	// so clients that connected right after bootstrap success are
	// served here instead of seeing a dead socket/pipe (B16).
	if d.handoff != nil {
		h := d.handoff
		d.handoff = nil
		stop := h.Adopt(d.api.Handler())
		log.Printf("skein: control listening on %s (adopted from bootstrap mode)", h.ln.Addr())
		go func() {
			<-ctx.Done()
			stop()
		}()
		return stop, nil
	}
	ln, label, remove, err := d.controlListener(ctx)
	if err != nil {
		return nil, err
	}
	if ln == nil {
		log.Printf("skein: warning: control transport unconfigured; running without a control plane (dev mode only)")
		return func() {}, nil
	}
	log.Printf("skein: control listening on %s", label)

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
// ConnContext is api.CallerContext: without it, kernel caller identity
// (SO_PEERCRED uid / pipe SID) never reaches handlers and every
// socket/pipe request fails authorisation (Step 1's wrapRole gate).
// This was the defect in the pre-Step-2 StartControlListeners that the
// live serveControl paths did not have; the surviving listener copies
// the live paths' wiring.
func controlServer(ctx context.Context, handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ConnContext:       api.CallerContext,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}
