//go:build unix

package daemon

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// testDaemon builds a daemon whose only control transport is a Unix
// socket at a temp path, with no dev-TCP listener and no discovery.
func testDaemon(t *testing.T, socket string) *Daemon {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	cfg := conf.Default()
	cfg.API.Listen = ""
	cfg.API.Socket = socket
	cfg.API.Token = make([]byte, 32)
	cfg.Node.NetworkSecret = make([]byte, 32)
	cfg.Discovery.MDNS = false
	cfg.Discovery.STUN = false
	d, err := New(cfg, id)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func unixHTTPClient(socket string) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
}

// TestSingleListenerCarriesCaller proves the surviving listener wires
// ConnContext (Step 1's authorisation gate depends on it): a request
// through StartControlListeners reaches the API as a kernel caller and
// gets 200 on a read endpoint, and a second listener on the same
// socket cannot start — there is exactly one live listener path.
func TestSingleListenerCarriesCaller(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	d := testDaemon(t, socket)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := d.StartControlListeners(ctx)
	if err != nil {
		t.Fatalf("StartControlListeners: %v", err)
	}
	defer stop()

	resp, err := unixHTTPClient(socket).Get("http://localhost/api/v1/status")
	if err != nil {
		t.Fatalf("GET status: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (caller identity missing -> 401)", resp.StatusCode)
	}

	if _, err := d.StartControlListeners(ctx); err == nil {
		t.Fatal("second StartControlListeners = nil, want bind failure (one listener only)")
	} else if !strings.Contains(err.Error(), "busy") {
		t.Fatalf("second listener error = %v, want ErrSocketBusy", err)
	}
}

// TestRunBindFailureIsFatal proves a configured control transport
// that fails to bind kills Run before any mesh work starts, instead
// of the old serveControl behaviour (log and run headless).
func TestRunBindFailureIsFatal(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	occupier, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer occupier.Close()

	d := testDaemon(t, socket)
	err = d.Run(context.Background())
	if err == nil {
		t.Fatal("Run with a busy control socket = nil, want fatal error")
	}
	if !strings.Contains(err.Error(), "control listener") {
		t.Fatalf("Run error = %v, want a control-listener error", err)
	}
}
