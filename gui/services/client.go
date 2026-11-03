package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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

// ControlClient returns the shared daemon client, creating it lazily.
func ControlClient() (*Client, error) {
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
		var e api.APIError
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e)
		if e.Message == "" {
			e.Message = resp.Status
		}
		return &APIError{Code: e.Code, Message: e.Message, Remediation: e.Remediation}
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
type APIError struct {
	Code        string
	Message     string
	Remediation string
}

func (e *APIError) Error() string {
	if e.Remediation != "" {
		return e.Message + ": " + e.Remediation
	}
	return e.Message
}

func controlSocketPath() string {
	if p := os.Getenv("SKEIN_CONTROL_SOCKET"); p != "" {
		return p
	}
	return "/run/skein/control.sock"
}

var _ = controlPipeName

func controlPipeName() string {
	if p := os.Getenv("SKEIN_CONTROL_PIPE"); p != "" {
		return p
	}
	return `\\.\pipe\skein-control`
}
