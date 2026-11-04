//go:build unix

package daemon

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// TestConnErrorKeepsControlPlaneAlive is the Step 5 (M5) test: a
// terminal dataplane error (here: the underlay UDP port is already
// bound, so startDataPlane fails) must put the daemon in ConnError
// WITHOUT killing the process — the control socket still answers
// status showing the error — and a config that also sets api.listen
// must NOT open a TCP control plane beside the socket.
func TestConnErrorKeepsControlPlaneAlive(t *testing.T) {
	// Keep the persisted paused flag out of the system path: the
	// retry path writes it when it settles, and this test must not
	// touch /var/lib/skein on the host.
	t.Setenv("SKEIN_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	// Occupy a UDP port on both families, so link.Listen fails
	// deterministically regardless of wildcard resolution.
	wild, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer wild.Close()
	port := wild.LocalAddr().(*net.UDPAddr).Port
	v4, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err == nil {
		defer v4.Close()
	}
	v6, err := net.ListenPacket("udp6", fmt.Sprintf("[::]:%d", port))
	if err == nil {
		defer v6.Close()
	}

	// A free TCP port for the api.listen that must NOT be served.
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr := tcpLn.Addr().String()
	tcpLn.Close()

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "control.sock")
	cfg := conf.Default()
	cfg.API.Listen = tcpAddr // service-style config: socket AND listen
	cfg.API.Socket = socket
	cfg.API.Token = make([]byte, 32)
	cfg.Node.NetworkSecret = make([]byte, 32)
	cfg.Node.ListenPort = uint16(port)
	cfg.Discovery.MDNS = false
	cfg.Discovery.STUN = false
	d, err := New(cfg, id)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- d.Run(ctx) }()

	// The dataplane must fail into ConnError, not exit Run.
	deadline := time.Now().Add(10 * time.Second)
	for d.Connection().State != ConnError {
		if time.Now().After(deadline) {
			t.Fatalf("connection state = %q, want %q (Run returned: %v)", d.Connection().State, ConnError, chanErr(runErr))
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case err := <-runErr:
		t.Fatalf("Run returned %v in ConnError, want it alive with the control plane up", err)
	default:
	}

	// Control plane still answers, showing the error.
	resp, err := unixHTTPClient(socket).Get("http://localhost/api/v1/status")
	if err != nil {
		t.Fatalf("GET status in ConnError: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET status in ConnError = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"state":"error"`) {
		t.Fatalf("status body lacks connection error state: %s", body)
	}
	if d.Connection().LastError == "" {
		t.Fatal("ConnError snapshot has no last_error for diagnostics")
	}

	// No second TCP control plane: the api.listen port must refuse.
	conn, err := net.DialTimeout("tcp", tcpAddr, 2*time.Second)
	if err == nil {
		conn.Close()
		t.Fatalf("TCP dial to api.listen %s succeeded, want connection refused (service config must be socket-only)", tcpAddr)
	}

	// Retry: free the underlay port, Connect, and the mesh side must
	// be re-attempted without the process dying. The settled state
	// may be connected (if TUN/netcfg work in this VM) or error
	// again (if they do not) — either proves the retry path ran and
	// Run stayed alive; the test must not claim a live mesh here.
	wild.Close()
	if v4 != nil {
		v4.Close()
	}
	if v6 != nil {
		v6.Close()
	}
	st, err := d.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect retry: %v", err)
	}
	t.Logf("after Connect retry, connection state = %q (connected or error both prove the retry ran)", st.State)
	if st.State == ConnConnecting {
		t.Fatalf("Connect retry left state connecting, want a settled state")
	}
	select {
	case err := <-runErr:
		t.Fatalf("Run returned %v after Connect retry, want it alive", err)
	default:
	}

	// Control plane still answers after the retry, then ctx cancel
	// ends Run cleanly (the only way Run now returns from ConnError).
	resp, err = unixHTTPClient(socket).Get("http://localhost/api/v1/status")
	if err != nil {
		t.Fatalf("GET status after retry: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET status after retry = %d, want 200", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run after ctx cancel = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestDevTCPServesGatedHandler is the complement of the test above:
// a pure dev config (listen set, no socket/pipe) DOES get its TCP
// control plane — but through the same wrapRole-gated handler, so an
// unauthenticated request is 401 and a bearer request can read but
// cannot mutate. The dataplane is forced into ConnError (occupied
// port) so Run stays blocked in the test without a live mesh.
func TestDevTCPServesGatedHandler(t *testing.T) {
	t.Setenv("SKEIN_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	wild, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer wild.Close()
	port := wild.LocalAddr().(*net.UDPAddr).Port

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr := tcpLn.Addr().String()
	tcpLn.Close()

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	token := make([]byte, 32)
	for i := range token {
		token[i] = byte(i + 1)
	}
	cfg := conf.Default()
	cfg.API.Listen = tcpAddr
	cfg.API.Socket = ""
	cfg.API.Pipe = ""
	cfg.API.Token = token
	cfg.Node.NetworkSecret = make([]byte, 32)
	cfg.Node.ListenPort = uint16(port)
	cfg.Discovery.MDNS = false
	cfg.Discovery.STUN = false
	d, err := New(cfg, id)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- d.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for d.Connection().State != ConnError {
		if time.Now().After(deadline) {
			t.Fatalf("connection state = %q, want %q", d.Connection().State, ConnError)
		}
		time.Sleep(20 * time.Millisecond)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	// No token: 401 — the TCP plane is authenticated, not open.
	resp, err := client.Get("http://" + tcpAddr + "/api/v1/status")
	if err != nil {
		t.Fatalf("GET status over dev TCP: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("GET status over dev TCP without token = %d, want 401", resp.StatusCode)
	}
	// Valid bearer token: read endpoint 200 (bearer is read-capped).
	req, _ := http.NewRequest(http.MethodGet, "http://"+tcpAddr+"/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+encodeToken(token))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET status over dev TCP with token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET status over dev TCP with token = %d, want 200", resp.StatusCode)
	}
	// Same bearer token, secret endpoint: 403 — bearer never exceeds read.
	req, _ = http.NewRequest(http.MethodPost, "http://"+tcpAddr+"/api/v1/invites/network", nil)
	req.Header.Set("Authorization", "Bearer "+encodeToken(token))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST invites/network over dev TCP: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("POST invites/network over dev TCP with bearer = %d, want 403", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run after ctx cancel = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func encodeToken(token []byte) string {
	return base64.RawURLEncoding.EncodeToString(token)
}

func chanErr(ch chan error) error {
	select {
	case err := <-ch:
		return err
	default:
		return nil
	}
}
