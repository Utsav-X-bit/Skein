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
// On success it does NOT close the listener: it returns the outcome and
// keeps the transport bound in a ControlHandoff for the normal daemon
// to adopt, so a client connecting immediately after success is served
// by the normal API instead of racing a close/rebind window. It creates
// no TUN device, binds no UDP port, starts no mDNS, and touches no
// firewall rule — the handler is the whole daemon in this mode. A
// second Serve call after success returns the outcome immediately.
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
		ConnContext:       api.CallerContext,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	serveErr := make(chan error, 1)
	h := newControlHandoff(srv, ln, b.Handler(), b.opts.Socket, serveErr)
	b.mu.Lock()
	b.handoff = h
	b.mu.Unlock()
	go func() {
		serveErr <- srv.Serve(ln)
	}()
	select {
	case <-b.success:
		out, _ := b.Outcome()
		return out, nil
	case <-b.closed:
		if out, ok := b.Outcome(); ok {
			return out, nil
		}
		h.Stop()
		return BootstrapOutcome{}, context.Canceled
	case <-ctx.Done():
		if out, ok := b.Outcome(); ok {
			return out, nil
		}
		h.Stop()
		return BootstrapOutcome{}, ctx.Err()
	}
}
