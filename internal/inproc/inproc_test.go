package inproc_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/inproc"
)

// TestDialServesWithCallerContext is the in-process transport contract:
// a client dialling the listener reaches an http.Server whose
// ConnContext carries the app's own UID as a kernel caller — the same
// identity the Unix socket derives from SO_PEERCRED.
func TestDialServesWithCallerContext(t *testing.T) {
	ln := inproc.NewListener()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := api.CallerFrom(r.Context())
		fmt.Fprintf(w, "uid=%d transport=%d known=%v", c.UID, c.Transport, c.Known())
	})
	srv := &http.Server{Handler: handler, ConnContext: api.CallerContext}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return ln.Dial(ctx)
			},
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Get("http://inproc/uid")
	if err != nil {
		t.Fatalf("GET over inproc listener: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	want := fmt.Sprintf("uid=%d transport=%d known=%v", os.Getuid(), api.TransportKernel, true)
	if string(raw) != want {
		t.Fatalf("caller context: got %q, want %q", raw, want)
	}
}

// TestDialAfterCloseFails: a closed listener refuses new dials instead
// of hanging the caller.
func TestDialAfterCloseFails(t *testing.T) {
	ln := inproc.NewListener()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := ln.Dial(ctx); err == nil {
		t.Fatal("Dial on a closed listener succeeded, want an error")
	}
}
