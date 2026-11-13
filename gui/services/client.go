package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// Client talks to the daemon over the local control transport. The GUI never
// reads the config or a bearer token; the socket/pipe authenticates the OS
// user in the kernel.
type Client struct {
	hc   *http.Client
	base string
}

var defaultClient *Client
var clientMu sync.Mutex

// ControlClient returns the shared daemon client, creating it lazily.
func ControlClient() (*Client, error) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if defaultClient != nil {
		return defaultClient, nil
	}
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialControl(ctx)
			},
			// Each pipe request gets a fresh connection; the daemon serves
			// one HTTP request per pipe instance.
			DisableKeepAlives: true,
		},
	}
	defaultClient = &Client{hc: hc, base: "http://skein"}
	return defaultClient, nil
}

// do performs a request and decodes the JSON response. A nil out discards it.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonNotConnected, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var e api.APIError
		_ = json.Unmarshal(raw, &e)
		if e.Message == "" {
			// Not every daemon error is the JSON envelope: the
			// bootstrap endpoints answer with http.Error plain
			// text ("network invite is truncated"), and the
			// bootstrap-mode catch-all does the same. Surface the
			// daemon's own words instead of the bare HTTP status,
			// so the UI can say WHY a join failed.
			e.Message = strings.TrimSpace(string(raw))
			if e.Message == "" {
				e.Message = resp.Status
			}
		}
		// Step 8: keep the HTTP status on the error so callers can
		// classify 401/403/423 distinctly (see errors.go) instead of
		// collapsing them into one "failed" toast.
		return &APIError{Code: e.Code, Status: resp.StatusCode, Message: e.Message, Remediation: e.Remediation}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// stream opens an SSE response.
func (c *Client) stream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDaemonNotConnected, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("daemon: %s", resp.Status)
	}
	return resp.Body, nil
}

// APIError is a daemon error with a stable code and remediation.
// Status is the HTTP status (0 only when the error was constructed
// locally, never from a daemon response); Kind() classifies it.
type APIError struct {
	Code        string
	Status      int
	Message     string
	Remediation string
}

func (e *APIError) Error() string {
	if e.Remediation != "" {
		return e.Message + ": " + e.Remediation
	}
	return e.Message
}

// expectedDaemonImage returns the daemon image path clients verify the
// server against (Step 2). Empty means the default rule: the server
// image basename must be skein/skeind (Unix) or skein.exe/skeind.exe
// (Windows). M6 (F-04): in a release build the SKEIN_DAEMON_IMAGE
// variable is ignored — an attacker-controlled value could otherwise
// point verification at a planted daemon image. A dev build
// (-tags skeindev) may set it, and tests use daemonImageOverride;
// it selects which image to expect, never to skip verification.
func expectedDaemonImage() string {
	if daemonImageOverride != "" {
		return daemonImageOverride
	}
	return securityEnv("SKEIN_DAEMON_IMAGE")
}

func controlSocketPath() string {
	if controlSocketOverride != "" {
		return controlSocketOverride
	}
	// M6 (F-04): release builds ignore SKEIN_CONTROL_SOCKET.
	if p := securityEnv("SKEIN_CONTROL_SOCKET"); p != "" {
		return p
	}
	return "/run/skein/control.sock"
}

var _ = controlPipeName

func controlPipeName() string {
	if controlPipeOverride != "" {
		return controlPipeOverride
	}
	// M6 (F-04): release builds ignore SKEIN_CONTROL_PIPE.
	if p := securityEnv("SKEIN_CONTROL_PIPE"); p != "" {
		return p
	}
	return `\\.\pipe\skein-control`
}
