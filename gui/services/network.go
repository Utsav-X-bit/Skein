package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// pingOutputLimit caps the combined stdout/stderr kept from a ping run. The
// operator sees the tail of a failure, never an unbounded dump.
const pingOutputLimit = 4 << 10

// pingTimeout bounds one probe; a hung ping must not hang the GUI.
const pingTimeout = 15 * time.Second

// newPingCommand is the command factory seam. Production uses
// exec.CommandContext; tests replace it with a fake process.
var newPingCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// PingResult reports one bounded overlay ping.
type PingResult struct {
	Address   string `json:"address"`
	Reachable bool   `json:"reachable"`
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
}

// NetworkService reports the mesh: status, routes, activity.
type NetworkService struct{}

func (s *NetworkService) Status(ctx context.Context) (api.Status, error) {
	c, err := ControlClient()
	if err != nil {
		return api.Status{}, err
	}
	var out api.Status
	err = c.do(ctx, "GET", "/api/v1/status", nil, &out)
	return out, err
}

func (s *NetworkService) Routes(ctx context.Context) ([]api.Route, error) {
	c, err := ControlClient()
	if err != nil {
		return nil, err
	}
	var out []api.Route
	err = c.do(ctx, "GET", "/api/v1/routes", nil, &out)
	return out, err
}

func (s *NetworkService) Links(ctx context.Context) ([]api.Link, error) {
	c, err := ControlClient()
	if err != nil {
		return nil, err
	}
	var out []api.Link
	err = c.do(ctx, "GET", "/api/v1/links", nil, &out)
	return out, err
}

func (s *NetworkService) Activities(ctx context.Context) ([]api.Activity, error) {
	c, err := ControlClient()
	if err != nil {
		return nil, err
	}
	var out []api.Activity
	err = c.do(ctx, "GET", "/api/v1/activities", nil, &out)
	return out, err
}

// Ping probes one overlay IP with the system ping binary. The address is
// parsed and re-rendered, so a hostname, flag, or shell fragment can never
// reach argv. A non-zero ping exit is a result (unreachable), not an error;
// only validation and spawn failures are errors.
func (s *NetworkService) Ping(ctx context.Context, ip string) (PingResult, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return PingResult{}, fmt.Errorf("ping: %q is not an IP address", ip)
	}
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	flag := "-c"
	if runtime.GOOS == "windows" {
		flag = "-n"
	}
	cmd := newPingCommand(ctx, "ping", flag, "4", addr.String())
	var out cappedBuffer
	out.max = pingOutputLimit
	cmd.Stdout = &out
	cmd.Stderr = &out

	runErr := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return PingResult{}, fmt.Errorf("ping %s: %w", addr, ctxErr)
	}
	res := PingResult{Address: addr.String(), Output: out.String()}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return PingResult{}, fmt.Errorf("ping %s: %w", addr, runErr)
	}
	res.Reachable = true
	return res, nil
}

// cappedBuffer keeps at most max bytes but always reports a full write so the
// child process never sees a short write as an error.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
