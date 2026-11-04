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
// binds the bootstrap named pipe and serves ONLY the bootstrap
// handler until one bootstrap succeeds or ctx is cancelled. Same
// guarantees: no TUN, no UDP, no mDNS, no firewall, graceful
// shutdown after the successful response. Windows runtime is
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
