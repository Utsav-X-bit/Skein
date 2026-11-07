//go:build windows

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

// Serve is the Windows counterpart of bootstrap_serve_unix.go: it
// binds the bootstrap named pipe and serves ONLY the bootstrap handler
// until one bootstrap succeeds or ctx is cancelled. On success it keeps
// the pipe bound in a ControlHandoff for the normal daemon to adopt, so
// a client that connects immediately after success does not see "The
// pipe has been ended" while the normal pipe is being created. Same
// guarantees: no TUN, no UDP, no mDNS, no firewall. Windows runtime is
// compile-checked only here (M3 Tier 5).
func (b *Bootstrap) Serve(ctx context.Context) (BootstrapOutcome, error) {
	if b.opts.Pipe == "" {
		return BootstrapOutcome{}, errors.New("daemon: bootstrap needs a control pipe name")
	}
	if out, ok := b.Outcome(); ok {
		return out, nil
	}
	ln, err := api.NewPipeListener(ctx, b.opts.Pipe, b.opts.AccessPath, api.DefaultMaxConns)
	if err != nil {
		return BootstrapOutcome{}, fmt.Errorf("daemon: bootstrap control pipe %s: %w", b.opts.Pipe, err)
	}
	srv := &http.Server{
		ConnContext:       api.CallerContext,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	serveErr := make(chan error, 1)
	h := newControlHandoff(srv, ln, b.Handler(), "", serveErr)
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
