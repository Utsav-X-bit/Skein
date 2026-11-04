//go:build !windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// Serve binds the bootstrap control socket and serves ONLY the
// bootstrap handler until one bootstrap succeeds or ctx is cancelled.
// On success it shuts the listener down gracefully (in-flight
// requests, including the successful bootstrap response, complete
// first) and returns the outcome for the in-process transition. It
// creates no TUN device, binds no UDP port, starts no mDNS, and
// touches no firewall rule — the handler is the whole daemon in this
// mode. A second Serve call after success returns the outcome
// immediately.
func (b *Bootstrap) Serve(ctx context.Context) (BootstrapOutcome, error) {
	if b.opts.Socket == "" {
		return BootstrapOutcome{}, errors.New("daemon: bootstrap needs a control socket path")
	}
	if out, ok := b.Outcome(); ok {
		return out, nil
	}
	ln, err := api.NewUnixListener(b.opts.Socket, controlSocketGroup, b.opts.AccessPath)
	if err != nil {
		return BootstrapOutcome{}, fmt.Errorf("daemon: bootstrap control socket %s: %w", b.opts.Socket, err)
	}
	srv := &http.Server{
		Handler:           b.Handler(),
		ConnContext:       api.CallerContext,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan struct{})
	go func() {
		for {
			if _, ok := b.Outcome(); ok {
				close(done)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()
	select {
	case <-done:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		cancel()
		<-serveErr
		out, _ := b.Outcome()
		return out, nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		cancel()
		<-serveErr
		return BootstrapOutcome{}, ctx.Err()
	}
}
