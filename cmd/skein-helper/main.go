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

Verbs:
  init                  create identity, config, secret; install and start the service
  join                  configure this node from a network invite (stdin or --pipe)
  import                migrate a user-mode config into the system path
  install               register the system service and start it
  uninstall             stop and remove the system service
  start | stop | restart
  status                service state (unprivileged)
  rotate-secret         backup, stop, rotate the network secret, start;
                        the result carries the new join string
  reset                 stop, then back up (or --purge) config and identity
  grant-user <uid>      (Linux) persist uid in the access file and apply the socket ACL
  set-owner <user>      (Windows) rewrite config and control-pipe DACLs
  update-daemon         verify the signed manifest; with --consent, swap the binary

Options:
  --source <path>       import: the user-mode config file to migrate
  --manifest <path>     update-daemon: the signed manifest
  --consent             update-daemon: apply, not just check
  --pipe <name>         (Windows) read the JSON request from, and write the JSON
                        result to, the GUI-created named pipe. The request
                        carries secrets, which never appear in argv.
  --purge               reset: remove the files instead of backing them up
  --keep-identity       reset: keep identity.key
  --name <n> --port <p> --mtu <m> --prefix-v4 <cidr> --prefix-v6 <cidr>
  --mdns | --no-mdns --stun | --no-stun --stun-server <addr>
                        init options (also accepted in the --pipe request)
`

type options struct {
	source     string
	manifest   string
	consent    bool
	pipe       string
	request    string
	result     string
	purge      bool
	keepIdent  bool
	name       string
	port       uint16
	mtu        int
	prefixV4   string
	prefixV6   string
	mdns       *bool
	stun       *bool
	stunServer string
}

type initRequest struct {
	Name       string `json:"name"`
	PrefixV4   string `json:"prefix_v4"`
	PrefixV6   string `json:"prefix_v6"`
	Port       uint16 `json:"port"`
	MTU        int    `json:"mtu"`
	MDNS       *bool  `json:"mdns"`
	STUN       *bool  `json:"stun"`
	STUNServer string `json:"stun_server"`
}

type joinRequest struct {
	Invite string `json:"invite"`
}

func main() {
	var stdout bytes.Buffer
	code := run(os.Args[1:], &stdout, os.Stderr)
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
		return runPiped(verb, rest, opts, stdout, stderr)
	}

	privilegedVerb := verb != "status" && verb != "help" && verb != "-h" && verb != "--help"
	if err := requirePrivilege(privilegedVerb); err != nil {
		return fail(stdout, err)
	}
	switch verb {
	case "init":
		var req initRequest
		if err := decodeRequest(requestData(opts), &req); err != nil {
			return fail(stdout, err)
		}
		mergeInitOptions(&opts, req)
		res, err := app.InitRoot(initOptions(opts))
		return result(stdout, err, res)
	case "join":
		invite, err := readInvite(requestData(opts))
		if err != nil {
			return fail(stdout, err)
		}
		res, err := app.JoinRoot(invite)
		return result(stdout, err, res)
	case "import":
		if opts.source == "" {
			return fail(stdout, errors.New("import needs --source <path>"))
		}
		res, err := app.ImportRoot(opts.source)
		return result(stdout, err, res)
	case "install":
		st, err := app.EnsureService()
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
	case "status":
		st, err := service.ReadStatus()
		if err != nil {
			return fail(stdout, err)
		}
		return result(stdout, nil, map[string]any{"state": string(st.State), "enabled": st.Enabled})
	case "rotate-secret":
		res, err := app.RotateRoot()
		return result(stdout, err, res)
	case "reset":
		res, err := app.ResetRoot(opts.purge, opts.keepIdent)
		return result(stdout, err, res)
	case "grant-user":
		if len(rest) != 1 {
			return fail(stdout, errors.New("usage: skein-helper grant-user <uid>"))
		}
		uid, err := strconv.Atoi(rest[0])
		if err != nil {
			return fail(stdout, fmt.Errorf("uid %q must be an integer", rest[0]))
		}
		if err := app.GrantUserRoot(uid); err != nil {
			return fail(stdout, err)
		}
		return result(stdout, nil, map[string]any{"uid": uid})
	case "set-owner":
		if len(rest) != 1 {
			return fail(stdout, errors.New("usage: skein-helper set-owner <user>"))
		}
		if err := app.SetOwnerRoot(rest[0]); err != nil {
			return fail(stdout, err)
		}
		return result(stdout, nil, map[string]any{"user": rest[0]})
	case "update-daemon":
		if opts.manifest == "" {
			return fail(stdout, errors.New("update-daemon needs --manifest <path>"))
		}
		res, err := app.UpdateRoot(opts.manifest, opts.consent)
		return result(stdout, err, res)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "skein-helper: unknown verb %q\n\n%s", verb, usage)
		return 2
	}
}

// runPiped serves one request over the GUI-created pipe: the request JSON
// (which may carry the join string) is read from the pipe, the verb runs,
// and the result JSON goes back. The buffer is zeroed after the call.
func runPiped(verb string, rest []string, opts options, stdout, stderr io.Writer) int {
	if err := requirePrivilege(true); err != nil {
		fail(stdout, err)
		return 1
	}
	session, err := openPipeSession(opts.pipe)
	if err != nil {
		fail(stdout, err)
		return 1
	}
	data, err := session.readRequest()
	if err != nil {
		fail(stdout, err)
		return 1
	}
	defer zero(data)

	var out bytes.Buffer
	var rc int
	switch verb {
	case "init":
		var req initRequest
		if err := json.Unmarshal(data, &req); err != nil {
			rc = fail(&out, fmt.Errorf("init request: %w", err))
		} else {
			mergeInitOptions(&opts, req)
			res, err := app.InitRoot(initOptions(opts))
			rc = result(&out, err, res)
		}
	case "join":
		var req joinRequest
		if err := json.Unmarshal(data, &req); err != nil {
			rc = fail(&out, fmt.Errorf("join request: %w", err))
		} else {
			res, err := app.JoinRoot(req.Invite)
			rc = result(&out, err, res)
		}
	case "rotate-secret":
		res, err := app.RotateRoot()
		rc = result(&out, err, res)
	default:
		fmt.Fprintf(stderr, "skein-helper: verb %q does not use the pipe channel\n", verb)
		return 2
	}
	if err := session.writeResult(out.Bytes()); err != nil {
		fmt.Fprintf(stderr, "skein-helper: pipe result: %v\n", err)
		return 1
	}
	return rc
}

// mergeInitOptions lets the --pipe request override the flags.
func mergeInitOptions(opts *options, req initRequest) {
	if req.Name != "" {
		opts.name = req.Name
	}
	if req.PrefixV4 != "" {
		opts.prefixV4 = req.PrefixV4
	}
	if req.PrefixV6 != "" {
		opts.prefixV6 = req.PrefixV6
	}
	if req.Port != 0 {
		opts.port = req.Port
	}
	if req.MTU != 0 {
		opts.mtu = req.MTU
	}
	if req.MDNS != nil {
		opts.mdns = req.MDNS
	}
	if req.STUN != nil {
		opts.stun = req.STUN
	}
	if req.STUNServer != "" {
		opts.stunServer = req.STUNServer
	}
}

func initOptions(o options) app.InitOptions {
	return app.InitOptions{
		Name:       o.name,
		PrefixV4:   o.prefixV4,
		PrefixV6:   o.prefixV6,
		Port:       o.port,
		MTU:        o.mtu,
		MDNS:       o.mdns,
		STUN:       o.stun,
		STUNServer: o.stunServer,
	}
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
		case "--source":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.source = v
		case "--manifest":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.manifest = v
		case "--consent":
			o.consent = true
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
			o.request = v
		case "--result":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.result = v
		case "--purge":
			o.purge = true
		case "--keep-identity":
			o.keepIdent = true
		case "--name":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.name = v
		case "--port":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			n, err := strconv.ParseUint(v, 10, 16)
			if err != nil {
				return o, rest, fmt.Errorf("--port: %q is not a port", v)
			}
			o.port = uint16(n)
		case "--mtu":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return o, rest, fmt.Errorf("--mtu: %q is not a number", v)
			}
			o.mtu = n
		case "--prefix-v4":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.prefixV4 = v
		case "--prefix-v6":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.prefixV6 = v
		case "--mdns":
			t := true
			o.mdns = &t
		case "--no-mdns":
			f := false
			o.mdns = &f
		case "--stun":
			t := true
			o.stun = &t
		case "--no-stun":
			f := false
			o.stun = &f
		case "--stun-server":
			v, err := next(arg)
			if err != nil {
				return o, rest, err
			}
			o.stunServer = v
		default:
			rest = append(rest, arg)
		}
	}
	return o, rest, nil
}

// stdinRequest returns the helper's stdin as the request source when it is a
// terminal (nothing piped), so a missing invite is a clean error.
// requestData reads the request from --request for an elevated helper, or
// from stdin for a direct invocation.
func requestData(o options) []byte {
	if o.request != "" {
		data, err := os.ReadFile(o.request)
		if err != nil {
			return nil
		}
		return data
	}
	return stdinRequest()
}

func stdinRequest() []byte {
	stat, _ := os.Stdin.Stat()
	if stat.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	return data
}

func decodeRequest(data []byte, out any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// readInvite takes the join string from piped stdin: one string, at most 2 KiB.
func readInvite(data []byte) (string, error) {
	invite := strings.TrimSpace(string(data))
	if invite == "" {
		return "", errors.New("join: pipe the network invite to stdin, e.g. echo 'skein:2:...' | skein-helper join")
	}
	if len(invite) > 2048 {
		return "", fmt.Errorf("join: invite is %d bytes, maximum is 2048", len(invite))
	}
	if strings.ContainsAny(invite, "\n\r\t ") {
		return "", errors.New("join: invite must be a single line")
	}
	return invite, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
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
		return "run import to migrate the existing config, or reset first"
	case strings.Contains(msg, "not running"), strings.Contains(msg, "service start"):
		return "check the service state with skein-helper status and the daemon logs"
	case strings.Contains(msg, "elevated"), strings.Contains(msg, "Administrator"), strings.Contains(msg, "root"):
		return "run the helper elevated (UAC or sudo)"
	case strings.Contains(msg, "config not found"):
		return "run init or import first"
	}
	return ""
}
