// skein-helper is the elevated fixed-verb helper for the Skein desktop app.
// Every verb validates its inputs and writes one JSON object to stdout:
// {"ok":true,...} or {"ok":false,"error":...,"remediation":...}. Secrets
// travel on stdin or a GUI-created pipe, never in argv.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/service"
)

const usage = `skein-helper — Skein desktop helper (elevated; installed root-owned)

Usage:
  skein-helper <verb> [options]

Retained verbs (Step 10: the minimal helper; everything else moved
to the daemon control API):
  bootstrap --verb init|join|import
                        (Step 4) mint a single-use bootstrap token and
                        start the service in bootstrap mode; the token
                        is returned ONLY in the JSON envelope (stdout,
                        or the GUI's verified pipe under --pipe), never
                        logged, never written to disk in plaintext.
                        Fails if a system config already exists or a
                        bootstrap is already in progress.
  install               register the system service and start it (with --daemon <path>: register that validated skeind instead of the package one)
  uninstall [--purge]   stop and remove the system service
  start | stop | restart
                        service lifecycle (restart is retained: the
                        secret-rotation endpoint sequence restarts
                        the service through it, and no daemon
                        endpoint restarts the service)
  enable | disable      start the service at boot / only on demand.
                        Registration is manual by default: the desktop
                        starts the service when it opens.
  status                service state and boot policy (unprivileged)
  grant-user <uid> [--role read|mutate|secret]
                        (Linux) persist uid in the access file and apply the socket ACL;
                        default role is mutate (explicit human elevation, not a
                        legacy bare line, which the API reads as read)
  revoke-user <uid> [--force]
                        (Linux) remove uid from the access file, remove
                        its socket ACL, and reload the live daemon's
                        grants without a restart. The invoking user
                        (PKEXEC_UID/SUDO_UID) must hold the secret role;
                        removing the last grant or last secret grant
                        needs --force. Revoking a uid with no grant
                        is an error, never a silent success.
  set-owner <user>      (Windows) rewrite config and control-pipe DACLs; restart the daemon afterwards
  revoke-user <user> [--force]
                        (Windows) remove the user's SID from the access
                        file and reload the live daemon's grants
                        without a restart; the invoking user must be
                        SYSTEM or the access-file owner.

Removed verbs (Step 10: each returns exit 1 with this replacement):
  init                  use: skein-helper bootstrap --verb init, then POST /api/v1/bootstrap/init with the token
  join                  use: skein-helper bootstrap --verb join, then POST /api/v1/bootstrap/join with the token
  import                use: skein-helper bootstrap --verb import, then POST /api/v1/bootstrap/import with the token
  rotate-secret         use: POST /api/v1/system/rotate-secret {"confirm":true} (secret role)
  reset                 use: POST /api/v1/system/reset {"confirm":true} (secret role), then skein-helper stop
  update-daemon         use: GET /api/v1/system/update/check?manifest_path=... then POST /api/v1/system/update/apply {"manifest_path":...,"confirm":true} (secret role)

Options:
  --daemon <path>       install: register this validated skeind instead of the package one
  --pipe <name>         (Windows) the GUI-created named pipe channel:
                        bootstrap returns its token envelope there, and
                        the removed-verb pointers are served there too.
  --purge               uninstall: accepted for the GUI/CLI uninstall
                        dialog; config purge itself is the daemon reset
                        endpoint above, not the helper.
  --role <role>         grant-user: read|mutate|secret
  --verb <verb>         bootstrap: init|join|import
  --force               revoke-user: allow removing the last grant or
                        the last secret grant
  --request <path>      read a JSON request from a file (elevated GUI path)
  --result <path>       write the JSON result to a file (refused for
                        bootstrap: the token is stdout-only)
`

// removedVerbs maps each Step 10-removed helper verb to the exact
// daemon endpoint that replaces it (Step 9 gate 5 route table is the
// source of truth: bootstrap init/join/import, system rotate-secret /
// reset / update check+apply). The pointer is the remediation: a user
// following it lands on a real, role-gated endpoint, not a dead verb.
// init/join/import point at the bootstrap endpoints, not at
// POST /api/v1/system/init — that endpoint always returns 409 for a
// configured node (Step 7), and the bootstrap flow is the only init
// path for an unconfigured node.
var removedVerbs = map[string]string{
	"init":          "skein-helper bootstrap --verb init, then POST /api/v1/bootstrap/init with the returned token",
	"join":          "skein-helper bootstrap --verb join, then POST /api/v1/bootstrap/join with the returned token",
	"import":        "skein-helper bootstrap --verb import, then POST /api/v1/bootstrap/import with the returned token",
	"rotate-secret": `POST /api/v1/system/rotate-secret {"confirm":true} (secret role)`,
	"reset":         `POST /api/v1/system/reset {"confirm":true} (secret role), then skein-helper stop`,
	"update-daemon": `GET /api/v1/system/update/check?manifest_path=... then POST /api/v1/system/update/apply {"manifest_path":...,"confirm":true} (secret role)`,
}

// removedVerb writes the Step 10 deprecation envelope and returns
// exit code 1. It is deliberately not fail(): fail() maps error text
// to a generic remediation, while this pointer must be exact.
func removedVerb(stdout io.Writer, verb string) int {
	fmt.Fprintf(stdout, "%s\n", mustJSON(map[string]any{
		"ok":          false,
		"error":       "verb removed: use the daemon control API",
		"verb":        verb,
		"remediation": removedVerbs[verb],
	}))
	return 1
}

type options struct {
	pipe    string
	request string
	result  string
	purge   bool
	force   bool
	role    string
	verb    string
	daemon  string
}

func main() {
	// Step 4: a bootstrap token is plaintext only in the stdout JSON
	// response to the invoking GUI. It must never be written to a
	// --result file on disk, so that combination is refused outright.
	if len(os.Args) > 1 && os.Args[1] == "bootstrap" && flagValue(os.Args[1:], "--result") != "" {
		fmt.Fprintln(os.Stderr, "skein-helper: bootstrap refuses --result: the token is returned in stdout JSON only, never written to a file")
		os.Exit(2)
	}
	var stdout bytes.Buffer
	code := run(os.Args[1:], &stdout, os.Stderr)
	if _, err := os.Stdout.Write(stdout.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "skein-helper: stdout: %v\n", err)
		code = 1
	}
	if path := flagValue(os.Args[1:], "--result"); path != "" {
		if err := writeResultFile(path, stdout.Bytes()); err != nil {
			fmt.Fprintf(os.Stderr, "skein-helper: result file: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

// flagValue returns the value of a repeated-free flag from argv.
func flagValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

// writeResultFile writes the helper's JSON output atomically for the GUI.
func writeResultFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".skein-helper-result-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// run dispatches one helper verb and writes the JSON result to stdout.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	verb, rest := args[0], args[1:]
	opts, rest, err := parseOptions(rest)
	if err != nil {
		fmt.Fprintf(stderr, "skein-helper: %v\n\n%s", err, usage)
		return 2
	}
	if opts.pipe != "" {
		if verb == "bootstrap" {
			return runBootstrapPiped(opts, stdout, stderr)
		}
		return runPiped(verb, rest, opts, stdout, stderr)
	}
	if _, removed := removedVerbs[verb]; removed {
		// Before any privilege check: a removed verb needs no
		// elevation to learn its replacement, and this keeps the
		// deprecation test passing on non-root CI runners too.
		return removedVerb(stdout, verb)
	}

	var privilegedVerb bool
	switch verb {
	case "bootstrap", "install", "uninstall", "start", "stop", "restart", "enable", "disable", "grant-user", "set-owner", "revoke-user":
		privilegedVerb = true
	case "status", "help", "-h", "--help":
	default:
		fmt.Fprintf(stderr, "skein-helper: unknown verb %q\n\n%s", verb, usage)
		return 2
	}
	if err := requirePrivilege(privilegedVerb); err != nil {
		return fail(stdout, err)
	}
	switch verb {
	case "bootstrap":
		return bootstrapVerb(opts, stdout)
	case "install":
		var st service.State
		var err error
		if opts.daemon != "" {
			st, err = app.EnsureServiceDaemon(opts.daemon)
		} else {
			st, err = app.EnsureService()
		}
		return result(stdout, err, map[string]any{"state": string(st)})
	case "uninstall":
		if err := uninstall(); err != nil {
			return fail(stdout, err)
		}
		return result(stdout, nil, map[string]any{"state": string(service.StateMissing)})
	case "start", "stop", "restart":
		var err error
		switch verb {
		case "start":
			err = service.Start()
		case "stop":
			err = service.Stop()
		case "restart":
			err = service.Restart()
		}
		if err != nil {
			return fail(stdout, err)
		}
		st, _ := service.ReadStatus()
		return result(stdout, nil, map[string]any{"state": string(st.State)})
	case "enable", "disable":
		var err error
		if verb == "enable" {
			err = service.Enable()
		} else {
			err = service.Disable()
		}
		if err != nil {
			return fail(stdout, err)
		}
		st, _ := service.ReadStatus()
		return result(stdout, nil, map[string]any{"enabled": st.Enabled})
	case "status":
		st, err := service.ReadStatus()
		if err != nil {
			return fail(stdout, err)
		}
		return result(stdout, nil, map[string]any{"state": string(st.State), "enabled": st.Enabled})
	case "revoke-user":
		if len(rest) != 1 {
			return fail(stdout, errors.New("usage: skein-helper revoke-user <uid-or-user> [--force]"))
		}
		out, err := app.RevokeUser(rest[0], opts.force)
		if err != nil {
			return fail(stdout, err)
		}
		payload := map[string]any{"identity": out.Identity, "role": out.Role, "reloaded": out.Reloaded}
		if out.Warning != "" {
			payload["warning"] = out.Warning
		}
		if out.ReloadNote != "" {
			payload["note"] = out.ReloadNote
		}
		return result(stdout, nil, payload)
	case "grant-user":
		if len(rest) != 1 {
			return fail(stdout, errors.New("usage: skein-helper grant-user <uid> [--role read|mutate|secret]"))
		}
		uid, err := strconv.Atoi(rest[0])
		if err != nil {
			return fail(stdout, fmt.Errorf("uid %q must be an integer", rest[0]))
		}
		role := opts.role
		if role == "" {
			role = "mutate" // plan default for a new explicit grant
		}
		if err := app.GrantUserRoleRoot(uid, role); err != nil {
			return fail(stdout, err)
		}
		return result(stdout, nil, map[string]any{"uid": uid, "role": role})
	case "set-owner":
		if len(rest) != 1 {
			return fail(stdout, errors.New("usage: skein-helper set-owner <user>"))
		}
		if err := app.SetOwnerRoot(rest[0]); err != nil {
			return fail(stdout, err)
		}
		// The new owner's grant and pipe access take effect at the
		// next daemon start (the live grant table and the pipe
		// listener's granted set are both built at start).
		return result(stdout, nil, map[string]any{"user": rest[0], "restart_required": true})
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "skein-helper: unknown verb %q\n\n%s", verb, usage)
		return 2
	}
}

// bootstrapVerb mints a single-use token and returns the JSON envelope.
// The token travels only in the caller's writer: stdout for an already
// elevated GUI/CLI, or the verified desktop pipe for a UAC launch.
func bootstrapVerb(opts options, stdout io.Writer) int {
	if opts.verb == "" {
		return fail(stdout, errors.New("bootstrap needs --verb init|join|import"))
	}
	token, err := app.MintBootstrap(opts.verb)
	if err != nil {
		return fail(stdout, err)
	}
	return result(stdout, nil, map[string]any{"token": token, "verb": opts.verb, "expires_in_seconds": 600})
}

// runBootstrapPiped is the UAC path: the unelevated desktop created a
// random, user-only named pipe; this elevated helper verifies that the
// pipe server really is the desktop, runs bootstrap, and writes the
// token envelope to that pipe. No token touches disk or argv.
func runBootstrapPiped(opts options, stdout, stderr io.Writer) int {
	// The pipe branch is dispatched before the verb table, so enforce
	// the same elevation the non-pipe bootstrap verb gets. A normal
	// user running this by hand must not reach MintBootstrap.
	if err := requirePrivilege(true); err != nil {
		return fail(stdout, err)
	}
	session, err := openPipeSession(opts.pipe)
	if err != nil {
		return fail(stdout, err)
	}
	if err := session.verifyDesktop(); err != nil {
		fmt.Fprintf(stderr, "skein-helper: bootstrap pipe: %v\n", err)
		return 1
	}
	var out bytes.Buffer
	code := bootstrapVerb(opts, &out)
	if err := session.writeResult(out.Bytes()); err != nil {
		fmt.Fprintf(stderr, "skein-helper: pipe result: %v\n", err)
		return 1
	}
	return code
}

// runPiped serves one request over the GUI-created pipe. bootstrap never
// reaches here (runBootstrapPiped handles it and verifies the desktop
// server first). Step 10: the only other verbs that ever used the pipe
// channel (init, join, rotate-secret) are removed, so the pipe serves
// only their endpoint pointers — no request body is read for a removed
// verb, and no privilege check gates learning the replacement. Anything
// else keeps the old "does not use the pipe channel" exit 2.
func runPiped(verb string, rest []string, opts options, stdout, stderr io.Writer) int {
	_ = rest
	session, err := openPipeSession(opts.pipe)
	if err != nil {
		fail(stdout, err)
		return 1
	}
	if _, removed := removedVerbs[verb]; removed {
		var out bytes.Buffer
		rc := removedVerb(&out, verb)
		if err := session.writeResult(out.Bytes()); err != nil {
			fmt.Fprintf(stderr, "skein-helper: pipe result: %v\n", err)
			return 1
		}
		return rc
	}
	fmt.Fprintf(stderr, "skein-helper: verb %q does not use the pipe channel\n", verb)
	return 2
}

// parseOptions consumes the per-verb flags and returns the leftover
// positional arguments.
func parseOptions(args []string) (options, []string, error) {
	var o options
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func(flag string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", flag)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "--pipe":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.pipe = v
		case "--request":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			// Parsed for GUI argv compatibility; no retained verb
			// reads a request file since Step 10 (the pipe verbs
			// that used it are removed).
			o.request = v
		case "--result":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.result = v
		case "--purge":
			// Accepted for the GUI/CLI uninstall dialog; config
			// purge itself is the daemon reset endpoint, not the
			// helper, so this flag changes no helper behaviour.
			o.purge = true
		case "--force":
			o.force = true
		case "--role":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.role = v
		case "--verb":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.verb = v
		case "--daemon":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.daemon = v
		default:
			rest = append(rest, arg)
		}
	}
	return o, rest, nil
}

// uninstall stops and removes the service and its firewall rules.
func uninstall() error {
	if cfg, err := conf.Load(app.SystemConfigPath()); err == nil {
		_ = firewall.Remove(firewall.Config{Interface: cfg.Node.Interface, Port: cfg.Node.ListenPort})
	}
	return service.Uninstall()
}

// fail writes the error envelope and returns the exit code.
func fail(stdout io.Writer, err error) int {
	fmt.Fprintf(stdout, "%s\n", mustJSON(map[string]any{
		"ok":          false,
		"error":       err.Error(),
		"remediation": remediation(err),
	}))
	return 1
}

// result writes the success envelope (or the error envelope) and returns the
// exit code.
func result(stdout io.Writer, err error, payload any) int {
	if err != nil {
		return fail(stdout, err)
	}
	m := map[string]any{"ok": true}
	switch v := payload.(type) {
	case map[string]any:
		for k, val := range v {
			m[k] = val
		}
	default:
		b, jerr := json.Marshal(v)
		if jerr != nil {
			return fail(stdout, jerr)
		}
		var fields map[string]any
		if jerr := json.Unmarshal(b, &fields); jerr != nil {
			return fail(stdout, jerr)
		}
		for k, val := range fields {
			m[k] = val
		}
	}
	fmt.Fprintf(stdout, "%s\n", mustJSON(m))
	return 0
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"ok":false,"error":"encode result: ` + err.Error() + `"}`
	}
	return string(b)
}

// remediation maps the common helper failures to the next step.
func remediation(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "already exists"):
		return "already configured; use the daemon control API (see skein-helper --help)"
	case strings.Contains(msg, "already in progress"):
		// Only what the state file proves: a token is on disk and
		// unexpired. It expires within the 10-minute TTL, and the file
		// is admin-owned with no cancel command, so name the wait and
		// the manual clear (stop the service first: a live bootstrap
		// daemon owns the state).
		return fmt.Sprintf("a bootstrap is already in progress; its token expires within 10 minutes, after which this succeeds. To clear it now: stop the service (Windows: Stop-Service skein, Linux: sudo systemctl stop skein), delete %s as administrator/root, then retry", app.BootstrapStatePath())
	case strings.Contains(msg, "not running"), strings.Contains(msg, "service start"):
		return "check the service state with skein-helper status and the daemon logs"
	case strings.Contains(msg, "elevated"), strings.Contains(msg, "Administrator"), strings.Contains(msg, "root"):
		return "run the helper elevated (UAC or sudo)"
	case strings.Contains(msg, "config not found"):
		return "run the bootstrap flow (skein-helper bootstrap --verb init|join|import) first"
	}
	return ""
}
