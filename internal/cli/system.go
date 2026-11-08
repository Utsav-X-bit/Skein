package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/ctlerr"
)

// Step 8 — system commands are thin clients of the daemon control API,
// never direct system-config writers. `skein system init|join|import`
// run the minimal helper's `bootstrap` verb to mint a single-use token,
// then call the daemon's bootstrap endpoints over the verified control
// transport; the token lives only in memory for that one call and is
// never logged (the same Go-string residual as every other secret in
// this codebase: it lingers until GC, it is never written to disk).
// `system reset|leave|rotate` call the Step 7 system endpoints, then
// perform the helper step the endpoint sequence requires (stop after
// reset, restart after rotate). Service lifecycle verbs go straight to
// the minimal helper. There is deliberately no `system` path that
// writes /etc/skein/skein.toml or identity.key itself — that is the
// Step 3 single-writer rule, and the Step 9 sweep will grep for it.

// newSystemClient builds the client for the service control plane. It
// is a variable so tests can substitute a fake daemon.
var newSystemClient = func() *client {
	cfg := conf.Default()
	app.ApplyControl(&cfg)
	return clientFor(cfg, 10*time.Second)
}

// runSystemHelper runs one minimal-helper verb and returns its stdout.
// The platform implementation elevates through the approved mechanism
// (pkexec with the shipped polkit action on Linux, UAC on Windows for
// non-secret verbs) instead of assuming the CLI is already root. It is
// a variable so tests can substitute a fake helper.
var runSystemHelper = execSystemHelper

// mintSystemToken mints a bootstrap token for verb via the helper. It is
// a variable so tests can substitute a token without a helper binary.
var mintSystemToken = func(verb string) (string, error) {
	raw, err := runSystemHelper("bootstrap", "--verb", verb)
	// The helper's fail() contract is JSON-on-stdout plus a non-zero
	// exit, so a failed mint still carries its error in raw. The old
	// code returned the bare exec error here and swallowed the
	// helper's message (M9 §2c: a Windows bootstrap failure surfaced
	// only as "exit status 1", hiding the real cause). Parse the
	// envelope first; fall back to the exec error only when the
	// output is not a helper envelope at all.
	var env struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		Remediation string `json:"remediation"`
		Token       string `json:"token"`
		Verb        string `json:"verb"`
	}
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("helper bootstrap: invalid JSON: %w", jerr)
	}
	if !env.OK {
		if env.Remediation != "" {
			return "", fmt.Errorf("helper bootstrap: %s: %s", env.Error, env.Remediation)
		}
		return "", fmt.Errorf("helper bootstrap: %s", env.Error)
	}
	if env.Token == "" {
		return "", fmt.Errorf("helper bootstrap: no token in response")
	}
	if env.Verb != "" && env.Verb != verb {
		return "", fmt.Errorf("helper bootstrap: token minted for %q, want %q", env.Verb, verb)
	}
	return env.Token, nil
}

// bootstrapResult posts one bootstrap request and decodes the daemon's
// BootstrapResult. The token is in the JSON body only, never in argv
// or a URL.
func bootstrapResult(ctx context.Context, c *client, verb, token string, fields map[string]any) (app.BootstrapResult, error) {
	var out app.BootstrapResult
	body := map[string]any{"token": token}
	for k, v := range fields {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return out, err
	}
	if err := c.doBodyWithResult(ctx, http.MethodPost, "/api/v1/bootstrap/"+verb, raw, &out); err != nil {
		return out, bootstrapDenied(err)
	}
	return out, nil
}

// bootstrapDenied explains a 403 on a bootstrap endpoint. Bootstrap is
// not role-gated: the daemon refuses when the kernel caller does not
// match the single-use token's bound uid/SID, or when the caller used
// the dev TCP bearer transport. The generic "ask for the secret role"
// text sends the user to the wrong fix, so replace it when the daemon
// supplied no remediation of its own. This rewrites guidance only; the
// status, message, token binding, and every fail-closed check are
// untouched.
func bootstrapDenied(err error) error {
	var ce *ctlerr.Error
	if !errors.As(err, &ce) || ce.Kind != ctlerr.Denied || ce.Remediation != "" {
		return err
	}
	return &ctlerr.Error{
		Kind:    ce.Kind,
		Code:    ce.Code,
		Message: ce.Message,
		Remediation: "the bootstrap token is bound to the user who approved the elevation prompt; " +
			"run this command as that same user (pkexec keeps the CLI unprivileged; on Windows use an elevated prompt). " +
			"Bootstrap is refused over the dev TCP bearer transport by design, and no role grant can fix a caller/token mismatch.",
	}
}

func printBootstrapResult(res app.BootstrapResult, stdout io.Writer) {
	fmt.Fprintf(stdout, "config:     %s\n", res.ConfigPath)
	fmt.Fprintf(stdout, "node id:    %s\n", res.NodeID)
	fmt.Fprintf(stdout, "public key: %s\n", res.PublicKey)
	fmt.Fprintf(stdout, "name:       %s\n", res.Name)
	fmt.Fprintf(stdout, "overlay:    %s\n", res.Overlay)
	if res.Invite != "" {
		fmt.Fprintf(stdout, "invite:     %s\n", res.Invite)
	}
	if res.Join != "" {
		fmt.Fprintf(stdout, "join:       %s\n", res.Join)
		fmt.Fprintln(stdout, "give the join string to the other nodes (it contains the network secret);")
		fmt.Fprintln(stdout, "each of them runs: skein system join <join string> (system install) or skein join <join string> (dev config)")
	}
	if res.Backup != "" {
		fmt.Fprintf(stdout, "backup:     %s\n", res.Backup)
	}
}

// cmdSystemInit runs the bootstrap init flow for a system install.
func cmdSystemInit(ctx context.Context, args []string, stdout io.Writer) error {
	fields := map[string]any{"name": defaultName(), "port": 11010, "mtu": 1280}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			i++
			fields["name"] = args[i]
		case "--port":
			if i+1 >= len(args) {
				return fmt.Errorf("--port requires a value")
			}
			i++
			p, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("--port: %w", err)
			}
			fields["port"] = p
		case "--mtu":
			if i+1 >= len(args) {
				return fmt.Errorf("--mtu requires a value")
			}
			i++
			m, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("--mtu: %w", err)
			}
			fields["mtu"] = m
		default:
			return fmt.Errorf("unknown system init flag %q", args[i])
		}
	}
	token, err := mintSystemToken("init")
	if err != nil {
		return err
	}
	res, err := bootstrapResult(ctx, newSystemClient(), "init", token, fields)
	if err != nil {
		return err
	}
	printBootstrapResult(res, stdout)
	return nil
}

// cmdSystemJoin runs the bootstrap join flow for a system install.
func cmdSystemJoin(ctx context.Context, invite string, stdout io.Writer) error {
	token, err := mintSystemToken("join")
	if err != nil {
		return err
	}
	res, err := bootstrapResult(ctx, newSystemClient(), "join", token, map[string]any{"invite": invite})
	if err != nil {
		return err
	}
	printBootstrapResult(res, stdout)
	return nil
}

// cmdSystemImport runs the bootstrap import flow for a system install.
func cmdSystemImport(ctx context.Context, source string, stdout io.Writer) error {
	token, err := mintSystemToken("import")
	if err != nil {
		return err
	}
	res, err := bootstrapResult(ctx, newSystemClient(), "import", token, map[string]any{"source": source})
	if err != nil {
		return err
	}
	printBootstrapResult(res, stdout)
	return nil
}

// cmdSystemReset calls the daemon reset endpoint, then stops the
// service via the helper (Step 7 sequence). The --force escape hatch
// is deleted: reset while the daemon runs goes through the API.
func cmdSystemReset(ctx context.Context, purge, keepIdentity bool, stdout io.Writer) error {
	c := newSystemClient()
	var res api.ResetResult
	body, _ := json.Marshal(api.ResetOptions{Purge: purge, KeepIdentity: keepIdentity, Confirm: true})
	if err := c.doBodyWithResult(ctx, http.MethodPost, "/api/v1/system/reset", body, &res); err != nil {
		return err
	}
	if res.Backup != "" {
		fmt.Fprintf(stdout, "reset: backup at %s\n", res.Backup)
	}
	if res.Message != "" {
		fmt.Fprintf(stdout, "reset: %s\n", res.Message)
	}
	if res.FirewallError != "" {
		fmt.Fprintf(stdout, "reset: warning: firewall cleanup failed: %s (rules may remain)\n", res.FirewallError)
	}
	if _, err := runSystemHelper("stop"); err != nil {
		return fmt.Errorf("reset complete but stopping the service failed: %w; stop it manually (systemctl stop skein / Stop-Service skein)", err)
	}
	fmt.Fprintln(stdout, "reset: service stopped; run `skein system init` or `skein system join <invite>` to set up again")
	return nil
}

// cmdSystemLeave leaves this node's own network (ADR-002: no peer
// removes another peer). Propagated is the source of truth: until M4
// lands leave-announcement gossip it is false, and the CLI says so.
func cmdSystemLeave(ctx context.Context, stdout io.Writer) error {
	c := newSystemClient()
	var res api.LeaveResult
	body, _ := json.Marshal(map[string]bool{"confirm": true})
	if err := c.doBodyWithResult(ctx, http.MethodPost, "/api/v1/system/leave", body, &res); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "left network: peers left %d, routes withdrawn %d\n", res.PeersLeft, res.RoutesWithdrawn)
	if !res.Propagated {
		fmt.Fprintln(stdout, "note: the leave has not yet been announced mesh-wide (leave gossip lands in M4); other peers will drop this node on timeout")
	}
	return nil
}

// cmdSystemRotate rotates the network secret via the daemon, prints
// the new join string exactly once, then restarts the service via
// the helper (Step 7 sequence). The join is printed before the
// restart attempt so a restart failure cannot lose it.
func cmdSystemRotate(ctx context.Context, stdout io.Writer) error {
	c := newSystemClient()
	var res api.RotateResult
	body, _ := json.Marshal(map[string]bool{"confirm": true})
	if err := c.doBodyWithResult(ctx, http.MethodPost, "/api/v1/system/rotate-secret", body, &res); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "join: %s\n", res.Join)
	fmt.Fprintln(stdout, "warning: every peer must rejoin with this join string (it contains the new network secret); the old secret no longer works")
	if res.Backup != "" {
		fmt.Fprintf(stdout, "backup: %s\n", res.Backup)
	}
	if _, err := runSystemHelper("restart"); err != nil {
		return fmt.Errorf("secret rotated and join string shown above, but restarting the service failed: %w; restart it manually", err)
	}
	fmt.Fprintln(stdout, "rotate: service restarted with the new secret")
	return nil
}

// systemHelperPath mirrors the GUI's helper lookup without importing
// the GUI package. M6 (F-04): in a release build SKEIN_HELPER is
// ignored and the packaged location is used; a dev build
// (-tags skeindev) may still override it.
func systemHelperPath() string {
	if p := securityEnv("SKEIN_HELPER"); p != "" {
		return p
	}
	return defaultSystemHelperPath()
}

// runSystemCommand dispatches `skein system <verb>`. It returns a
// process exit code and prints errors to stderr itself, matching Run.
func runSystemCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: skein system init|join|import|reset|leave|rotate|start|stop|restart")
		return 2
	}
	var err error
	switch args[0] {
	case "init":
		err = cmdSystemInit(ctx, args[1:], stdout)
	case "join":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: skein system join <invite>")
			return 2
		}
		err = cmdSystemJoin(ctx, args[1], stdout)
	case "import":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: skein system import <path>")
			return 2
		}
		err = cmdSystemImport(ctx, args[1], stdout)
	case "reset":
		purge, keepIdentity := false, false
		for _, arg := range args[1:] {
			switch arg {
			case "--purge":
				purge = true
			case "--keep-identity":
				keepIdentity = true
			default:
				fmt.Fprintf(stderr, "skein: unknown system reset flag %q (there is no --force: reset goes through the daemon API)\n", arg)
				return 2
			}
		}
		err = cmdSystemReset(ctx, purge, keepIdentity, stdout)
	case "leave":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: skein system leave")
			return 2
		}
		err = cmdSystemLeave(ctx, stdout)
	case "rotate":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: skein system rotate")
			return 2
		}
		err = cmdSystemRotate(ctx, stdout)
	case "start", "stop", "restart":
		if len(args) != 1 {
			fmt.Fprintf(stderr, "usage: skein system %s\n", args[0])
			return 2
		}
		_, err = runSystemHelper(args[0])
		if err == nil {
			fmt.Fprintf(stdout, "skein service %s\n", args[0])
		}
	default:
		fmt.Fprintf(stderr, "skein: unknown system command %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "skein: %v\n", err)
		return 1
	}
	return 0
}

// apiErrorBody parses a daemon JSON error envelope when present.
func apiErrorBody(raw []byte) (api.APIError, bool) {
	var e api.APIError
	if err := json.Unmarshal(raw, &e); err != nil || e.Message == "" {
		return e, false
	}
	return e, true
}

// classifiedAPIError converts an HTTP status + body into a ctlerr
// error, preserving the daemon's message and remediation.
func classifiedAPIError(status int, raw []byte) error {
	if e, ok := apiErrorBody(raw); ok {
		ce := ctlerr.New(status, e.Message)
		if e.Remediation != "" {
			ce.Remediation = e.Remediation
		}
		return ce
	}
	msg := strings.TrimSpace(string(raw))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return ctlerr.New(status, msg)
}
