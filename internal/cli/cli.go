// Package cli implements the skein command line client.
package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/path"
	"github.com/Utsav-X-bit/skein/internal/service"
	"github.com/Utsav-X-bit/skein/internal/version"
	"github.com/Utsav-X-bit/skein/internal/wan"
)

const (
	defaultIPv4Prefix = "10.144.144.0/24"
	defaultIPv6Prefix = "fd00:6d65:7368::/64"
)

const usage = `skein — Skein mesh VPN command line

Usage:
  skein [-c <config>] init                  create identity, config, secret, join string
  skein [-c <config>] join <invite>         configure this node from a network invite
  skein [-c <config>] name [value]          show or set this node's name
  skein [-c <config>] genkey                print the public key and node ID
  skein [-c <config>] invite [--network]    print a peer invite or a network join string
  skein [-c <config>] run                   run the daemon (root / Administrator)
  skein [-c <config>] install-service       register the system service
  skein [-c <config>] uninstall-service     remove the system service
  skein [-c <config>] dashboard             live mesh status; starts the daemon if needed
  skein [-c <config>] status [--json]
  skein [-c <config>] peers [list|add <invite>|rm <peer-id>] [--json]
  skein [-c <config>] routes [--json]
  skein [-c <config>] probe <peer-id>
  skein [-c <config>] ping <overlay-ip>
  skein version
`

// Run executes one CLI invocation and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseGlobal(args)
	if err != nil {
		fmt.Fprintf(stderr, "skein: %v\n\n%s", err, usage)
		return 2
	}
	path, rest, jsonOut := opts.path, opts.rest, opts.jsonOut
	if len(rest) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	switch rest[0] {
	case "init":
		if err := cmdInit(path, stdout); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		return 0
	case "genkey":
		if err := cmdGenkey(path, stdout); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		return 0
	case "invite":
		network := false
		for _, arg := range rest[1:] {
			if arg == "--network" {
				network = true
			}
		}
		if err := cmdInvite(path, network, stdout); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		return 0
	case "name":
		if len(rest) > 2 {
			fmt.Fprintln(stderr, "usage: skein name [value]")
			return 2
		}
		if err := cmdName(path, rest[1:], stdout); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		return 0
	case "join":
		if len(rest) != 2 {
			fmt.Fprintln(stderr, "usage: skein join <invite>")
			return 2
		}
		if err := cmdJoin(path, rest[1], stdout); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		return 0
	case "run":
		runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := app.Run(runCtx, path); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		return 0
	case "install-service":
		if err := app.InstallService(path); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		fmt.Printf("skein service installed\n  start: systemctl start %s (Linux) or sc start %s (Windows)\n", service.Name, service.Name)
		return 0
	case "uninstall-service":
		if err := app.UninstallService(path); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		fmt.Println("skein service removed")
		return 0
	case "dashboard", "top":
		cfg, err := loadConfig(path)
		if err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		motion := !opts.noMotion && os.Getenv("SKEIN_NO_MOTION") == ""
		d := newDashboard(cfg, stdout, !opts.noColor, motion)
		d.configPath = path
		if err := d.ensureDaemon(ctx, path); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		defer d.closeChild()
		if err := d.run(ctx); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		return 0
	case "status", "peers", "routes", "probe", "ping":
		cfg, err := loadConfig(path)
		if err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		if err := runOnline(ctx, cfg, rest, jsonOut, stdout); err != nil {
			fmt.Fprintf(stderr, "skein: %v\n", err)
			return 1
		}
		return 0
	case "version":
		fmt.Fprintln(stdout, "skein "+version.Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "skein: unknown command %q\n\n%s", rest[0], usage)
		return 2
	}
}

type globalOptions struct {
	path     string
	rest     []string
	jsonOut  bool
	noColor  bool
	noMotion bool
}

func parseGlobal(args []string) (globalOptions, error) {
	var opts globalOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c", "--config":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a path", args[i])
			}
			opts.path = args[i+1]
			i++
		case "--json":
			opts.jsonOut = true
		case "--no-color":
			opts.noColor = true
		case "--no-motion":
			opts.noMotion = true
		default:
			opts.rest = append(opts.rest, args[i])
		}
	}
	if opts.path == "" {
		var err error
		if opts.path, err = conf.DefaultPathForUser(); err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func loadConfig(path string) (conf.Config, error) {
	cfg, err := conf.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			// The daemon runs as root and may have rewritten files the user
			// cannot read; hand the whole directory back to them.
			return conf.Config{}, fmt.Errorf("load %s: permission denied; fix ownership with:\n  sudo chown -R $(id -u):$(id -g) %s", path, filepath.Dir(path))
		}
		return conf.Config{}, fmt.Errorf("load %s (run `skein init` first): %w", path, err)
	}
	return cfg, nil
}

func identityPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "identity.key")
}

func cmdInit(configPath string, stdout io.Writer) error {
	keyPath := identityPath(configPath)
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite", configPath)
	}
	if _, err := os.Stat(keyPath); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite", keyPath)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}

	id, err := identity.Generate()
	if err != nil {
		return err
	}
	psk := make([]byte, identity.KeySize)
	if _, err := rand.Read(psk); err != nil {
		return err
	}
	token := make([]byte, identity.KeySize)
	if _, err := rand.Read(token); err != nil {
		return err
	}

	if err := identity.Save(keyPath, id); err != nil {
		return err
	}
	hostname := defaultName()
	cfgText := fmt.Sprintf(`# Skein configuration. Edit overlay addresses before starting.
[node]
name                = %q
overlay_ipv4_prefix = "10.144.144.0/24"
overlay_ipv6_prefix = "fd00:6d65:7368::/64"
overlay_ipv4        = "10.144.144.1"
listen_port         = 11010
network_secret      = %q
mtu                 = 1280
interface           = %q

[api]
listen = "127.0.0.1:11011"
token  = %q

[discovery]
mdns = true
`, hostname, base64.RawURLEncoding.EncodeToString(psk), conf.DefaultInterface(), base64.RawURLEncoding.EncodeToString(token))

	f, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		os.Remove(keyPath)
		return err
	}
	if _, err := f.WriteString(cfgText); err != nil {
		f.Close()
		os.Remove(keyPath)
		os.Remove(configPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(keyPath)
		os.Remove(configPath)
		return err
	}
	if err := fileperm.Restrict(configPath); err != nil {
		os.Remove(keyPath)
		os.Remove(configPath)
		return err
	}
	_ = fileperm.ChownToSudoUser(configPath)
	_ = fileperm.ChownToSudoUser(keyPath)

	cands, _ := path.LocalCandidates(conf.DefaultListenPort)
	cands = path.ExcludePrefixes(cands, []netip.Prefix{
		netip.MustParsePrefix(defaultIPv4Prefix),
		netip.MustParsePrefix(defaultIPv6Prefix),
	})
	wanCfg := conf.Default()
	cands = wan.Merge(cands, wan.Candidates(context.Background(), wanCfg))
	inv := identity.Invite{ID: id.ID, PublicKey: id.PublicKey, Addrs: cands}
	invite, _ := identity.EncodeInvite(inv)

	joinCfg := conf.Default()
	joinCfg.Node.NetworkSecret = psk
	joinCfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.1")
	joinCfg.API.Token = token
	join, _ := encodeJoin(joinCfg, id, cands)

	fmt.Fprintf(stdout, "config:     %s\n", configPath)
	fmt.Fprintf(stdout, "identity:   %s\n", keyPath)
	fmt.Fprintf(stdout, "node id:    %016x\n", id.ID)
	fmt.Fprintf(stdout, "public key: %s\n", base64.StdEncoding.EncodeToString(id.PublicKey))
	fmt.Fprintf(stdout, "invite:     %s\n", invite)
	fmt.Fprintf(stdout, "join:       %s\n", join)
	fmt.Fprintln(stdout, "give the join string to the other nodes (it contains the network secret);")
	fmt.Fprintln(stdout, "each of them runs: skein join <join string>")
	return nil
}

// defaultName is the hostname, trimmed to the name limit.
func defaultName() string { return conf.DefaultNodeName() }

func cmdName(configPath string, args []string, stdout io.Writer) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if cfg.Node.Name == "" {
			fmt.Fprintln(stdout, "(no name set)")
			return nil
		}
		fmt.Fprintln(stdout, cfg.Node.Name)
		return nil
	}
	name := strings.TrimSpace(args[0])
	if name == "" {
		return errors.New("name must not be empty")
	}

	// A running daemon advertises the new name immediately; no restart.
	c := &client{
		base:  "http://" + cfg.API.Listen,
		token: base64.RawURLEncoding.EncodeToString(cfg.API.Token),
		http:  &http.Client{Timeout: 2 * time.Second},
	}
	if err := c.setName(context.Background(), name); err == nil {
		fmt.Fprintf(stdout, "name set to %q; advertised immediately\n", name)
		return nil
	}

	cfg.Node.Name = name
	if err := conf.Validate(cfg); err != nil {
		return err
	}
	text, err := conf.Render(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, text, 0o600); err != nil {
		return err
	}
	if err := fileperm.Restrict(configPath); err != nil {
		return err
	}
	_ = fileperm.ChownToSudoUser(configPath)
	fmt.Fprintf(stdout, "name set to %q; start or restart the daemon to advertise it\n", name)
	return nil
}

func cmdGenkey(configPath string, stdout io.Writer) error {
	keyPath := identityPath(configPath)
	id, err := identity.Load(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return err
		}
		if id, err = identity.Generate(); err != nil {
			return err
		}
		if err := identity.Save(keyPath, id); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "node id:    %016x\n", id.ID)
	fmt.Fprintf(stdout, "public key: %s\n", base64.StdEncoding.EncodeToString(id.PublicKey))
	return nil
}

func cmdInvite(configPath string, network bool, stdout io.Writer) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	id, err := identity.Load(identityPath(configPath))
	if err != nil {
		return err
	}
	cands, err := allCandidates(cfg)
	if err != nil {
		return err
	}
	if !network {
		invite, err := identity.EncodeInvite(identity.Invite{ID: id.ID, PublicKey: id.PublicKey, Addrs: cands})
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, invite)
		return nil
	}
	join, err := encodeJoin(cfg, id, cands)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, join)
	return nil
}

// encodeJoin builds the secret-bearing bootstrap string.
func encodeJoin(cfg conf.Config, id identity.Identity, cands []netip.AddrPort) (string, error) {
	used := []netip.Addr{cfg.Node.OverlayIPv4}
	used = append(used, runningPeerOverlays(cfg)...)
	used = dedupeOverlays(used, cfg.Node.OverlayIPv4Prefix)
	if len(used) > 16 {
		used = used[:16]
	}
	return identity.EncodeNetworkInvite(identity.NetworkInvite{
		Secret:    cfg.Node.NetworkSecret,
		ID:        id.ID,
		PublicKey: id.PublicKey,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
		UsedV4:    used,
		Addrs:     cands,
	})
}

// localCandidates lists this node's interface endpoints.
func localCandidates(cfg conf.Config) ([]netip.AddrPort, error) {
	cands, err := path.LocalCandidates(cfg.Node.ListenPort)
	if err != nil {
		return nil, err
	}
	return path.ExcludePrefixes(cands, []netip.Prefix{
		cfg.Node.OverlayIPv4Prefix,
		cfg.Node.OverlayIPv6Prefix,
	}), nil
}

// allCandidates adds public_addr and the STUN-discovered address.
func allCandidates(cfg conf.Config) ([]netip.AddrPort, error) {
	base, err := localCandidates(cfg)
	if err != nil {
		return nil, err
	}
	return wan.Merge(base, wan.Candidates(context.Background(), cfg)), nil
}

// runningPeerOverlays asks the local daemon for overlay addresses already in
// use. It returns nil when the daemon is not running.
func runningPeerOverlays(cfg conf.Config) []netip.Addr {
	c := &client{
		base:  "http://" + cfg.API.Listen,
		token: base64.RawURLEncoding.EncodeToString(cfg.API.Token),
		http:  &http.Client{Timeout: 2 * time.Second},
	}
	var peers []struct {
		OverlayIPv4 string `json:"overlay_ipv4"`
	}
	if err := c.get(context.Background(), "/api/v1/peers", &peers); err != nil {
		return nil
	}
	out := make([]netip.Addr, 0, len(peers))
	for _, p := range peers {
		if addr, err := netip.ParseAddr(p.OverlayIPv4); err == nil {
			out = append(out, addr)
		}
	}
	return out
}

func dedupeOverlays(addrs []netip.Addr, prefix netip.Prefix) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(addrs))
	out := addrs[:0]
	for _, addr := range addrs {
		if !addr.IsValid() || !prefix.Contains(addr) {
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}

// cmdJoin configures this node from a network invite: secret, overlay address,
// and the inviter as a dialer peer.
func cmdJoin(configPath, inviteText string, stdout io.Writer) error {
	inv, err := identity.DecodeNetworkInvite(inviteText)
	if err != nil {
		return err
	}

	cfg := conf.Default()
	cfg.Node.Name = defaultName()
	if _, statErr := os.Stat(configPath); statErr == nil {
		cfg, err = conf.Load(configPath)
		if err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}

	// The identity is needed before the overlay address is chosen: the address
	// is derived from it so stale invites do not hand out the same one twice.
	keyPath := identityPath(configPath)
	if _, statErr := os.Stat(keyPath); errors.Is(statErr, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return err
		}
		fresh, err := identity.Generate()
		if err != nil {
			return err
		}
		if err := identity.Save(keyPath, fresh); err != nil {
			return err
		}
		_ = fileperm.ChownToSudoUser(keyPath)
	}
	id, err := identity.Load(keyPath)
	if err != nil {
		return err
	}

	cfg.Node.NetworkSecret = inv.Secret
	cfg.Node.OverlayIPv4Prefix = inv.PrefixV4
	cfg.Node.OverlayIPv6Prefix = inv.PrefixV6

	alreadyJoined := false
	for _, p := range cfg.Peers {
		if p.ID == inv.ID {
			alreadyJoined = true
		}
	}
	// On a first join, avoid every address the inviter already knows. On a
	// re-join, keep the address this node was assigned.
	keepOverlay := alreadyJoined &&
		cfg.Node.OverlayIPv4.IsValid() &&
		inv.PrefixV4.Contains(cfg.Node.OverlayIPv4)
	if !keepOverlay {
		addr, err := conf.NextFreeOverlayHashed(inv.PrefixV4, inv.UsedV4, id.ID)
		if err != nil {
			return err
		}
		cfg.Node.OverlayIPv4 = addr
	}
	if len(cfg.API.Token) != 32 {
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return err
		}
		cfg.API.Token = token
	}

	inviter := conf.Peer{ID: inv.ID, PublicKey: inv.PublicKey, Addrs: inv.Addrs, Dialer: true}
	merged := false
	for i := range cfg.Peers {
		if cfg.Peers[i].ID == inv.ID {
			cfg.Peers[i] = inviter
			merged = true
			break
		}
	}
	if !merged {
		cfg.Peers = append(cfg.Peers, inviter)
	}
	if err := conf.Validate(cfg); err != nil {
		return err
	}

	text, err := conf.Render(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, text, 0o600); err != nil {
		return err
	}
	if err := fileperm.Restrict(configPath); err != nil {
		return err
	}
	_ = fileperm.ChownToSudoUser(configPath)
	_ = fileperm.ChownToSudoUser(keyPath)

	cands, _ := allCandidates(cfg)
	own, _ := identity.EncodeInvite(identity.Invite{ID: id.ID, PublicKey: id.PublicKey, Addrs: cands})
	fmt.Fprintf(stdout, "joined network\n")
	fmt.Fprintf(stdout, "  config:  %s\n", configPath)
	fmt.Fprintf(stdout, "  node id: %016x\n", id.ID)
	fmt.Fprintf(stdout, "  overlay: %s\n", cfg.Node.OverlayIPv4)
	fmt.Fprintf(stdout, "  inviter: %016x at %v\n", inv.ID, inv.Addrs)
	fmt.Fprintf(stdout, "  invite:  %s\n", own)
	if !alreadyJoined {
		fmt.Fprintln(stdout, "note: the join string listed the addresses in use when it was created;")
		fmt.Fprintln(stdout, "      regenerate it on the inviter before the next node joins, so that node")
		fmt.Fprintln(stdout, "      picks a free overlay address instead of reusing this one")
	}
	fmt.Fprintln(stdout, "start with: skein run -c <config>")
	return nil
}

func runOnline(ctx context.Context, cfg conf.Config, args []string, jsonOut bool, stdout io.Writer) error {
	c := &client{
		base:  "http://" + cfg.API.Listen,
		token: base64.RawURLEncoding.EncodeToString(cfg.API.Token),
		http:  &http.Client{Timeout: 10 * time.Second},
	}
	switch args[0] {
	case "status":
		return c.print(ctx, "/api/v1/status", jsonOut, stdout, printStatus)
	case "routes":
		return c.print(ctx, "/api/v1/routes", jsonOut, stdout, printRoutes)
	case "peers":
		return cmdPeers(ctx, c, args[1:], jsonOut, stdout)
	case "probe":
		if len(args) != 2 {
			return errors.New("usage: skein probe <peer-id>")
		}
		id, err := parsePeerID(args[1])
		if err != nil {
			return err
		}
		return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/peers/%016x/probe", id))
	case "ping":
		if len(args) != 2 {
			return errors.New("usage: skein ping <overlay-ip>")
		}
		return pingOverlay(ctx, args[1], stdout)
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func cmdPeers(ctx context.Context, c *client, args []string, jsonOut bool, stdout io.Writer) error {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		return c.print(ctx, "/api/v1/peers", jsonOut, stdout, printPeers)
	case "add":
		if len(args) != 2 {
			return errors.New("usage: skein peers add <invite>")
		}
		body, err := json.Marshal(map[string]string{"invite": args[1]})
		if err != nil {
			return err
		}
		return c.doBody(ctx, http.MethodPost, "/api/v1/peers", body)
	case "rm":
		if len(args) != 2 {
			return errors.New("usage: skein peers rm <peer-id>")
		}
		id, err := parsePeerID(args[1])
		if err != nil {
			return err
		}
		return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/peers/%016x", id))
	default:
		return fmt.Errorf("unknown peers action %q", action)
	}
}

func parsePeerID(s string) (uint64, error) {
	id, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("peer id %q must be 16 hexadecimal digits", s)
	}
	return id, nil
}

type client struct {
	base  string
	token string
	http  *http.Client
}

func (c *client) setName(ctx context.Context, name string) error {
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}
	return c.doBody(ctx, http.MethodPost, "/api/v1/name", body)
}

func (c *client) do(ctx context.Context, method, path string) error {
	return c.doBody(ctx, method, path, nil)
}

func (c *client) doBody(ctx context.Context, method, path string, body []byte) error {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("contact the daemon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("daemon: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (c *client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("contact the daemon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("daemon: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *client) print(ctx context.Context, path string, jsonOut bool, w io.Writer, render func(any, io.Writer) error) error {
	var raw json.RawMessage
	if err := c.get(ctx, path, &raw); err != nil {
		return err
	}
	if jsonOut {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			return err
		}
		fmt.Fprintln(w, buf.String())
		return nil
	}
	return render(raw, w)
}

type statusDTO struct {
	ID            string `json:"id"`
	OverlayIPv4   string `json:"overlay_ipv4"`
	OverlayIPv6   string `json:"overlay_ipv6"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Version       string `json:"version"`
	ListenPort    uint16 `json:"listen_port"`
	PeerCount     int    `json:"peers"`
	RxBytes       uint64 `json:"rx_bytes"`
	TxBytes       uint64 `json:"tx_bytes"`
}

func printStatus(raw any, w io.Writer) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var st statusDTO
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	fmt.Fprintf(w, "node id:  %s\n", st.ID)
	fmt.Fprintf(w, "overlay:  %s", st.OverlayIPv4)
	if st.OverlayIPv6 != "" {
		fmt.Fprintf(w, ", %s", st.OverlayIPv6)
	}
	fmt.Fprintf(w, "\npeers:    %d\nuptime:   %s\nversion:  %s\nport:     %d\n",
		st.PeerCount, time.Duration(st.UptimeSeconds)*time.Second, st.Version, st.ListenPort)
	return nil
}

type peerDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	State       string   `json:"state"`
	Path        string   `json:"path"`
	Transport   string   `json:"transport"`
	TransportRx string   `json:"transport_rx"`
	RTTMs       float64  `json:"rtt_ms"`
	OverlayIPv4 string   `json:"overlay_ipv4"`
	OverlayIPv6 string   `json:"overlay_ipv6"`
	Endpoint    string   `json:"endpoint"`
	Candidates  []string `json:"candidates"`
	Dialer      bool     `json:"dialer"`
	Direct      bool     `json:"direct"`
	RxBytes     uint64   `json:"rx_bytes"`
	TxBytes     uint64   `json:"tx_bytes"`
	LastSeenMS  int64    `json:"last_seen_ms"`
}

func printPeers(raw any, w io.Writer) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var peers []peerDTO
	if err := json.Unmarshal(b, &peers); err != nil {
		return err
	}
	if len(peers) == 0 {
		fmt.Fprintln(w, "no peers")
		return nil
	}
	fmt.Fprintf(w, "%-14s  %-16s  %-6s  %-4s  %-4s  %-10s  %s\n", "NAME", "ID", "STATE", "PATH", "NET", "RTT", "ENDPOINT")
	for _, p := range peers {
		name := p.Name
		if name == "" {
			name = "—"
		}
		fmt.Fprintf(w, "%-14s  %-16s  %-6s  %-4s  %-4s  %-10s  %s\n",
			name, p.ID, p.State, p.Path, p.Transport, fmt.Sprintf("%.1fms", p.RTTMs), p.Endpoint)
	}
	return nil
}

type routeDTO struct {
	Destination string   `json:"destination"`
	NextHop     string   `json:"next_hop"`
	CostMs      float64  `json:"cost_ms"`
	Hops        uint8    `json:"hops"`
	Path        []string `json:"path"`
}

type activityDTO struct {
	Origin      string   `json:"origin"`
	Destination string   `json:"destination"`
	Seq         uint64   `json:"seq"`
	RxBytes     uint64   `json:"rx_bytes"`
	TxBytes     uint64   `json:"tx_bytes"`
	RTTMs       float64  `json:"rtt_ms"`
	JitterMs    float64  `json:"jitter_ms"`
	Transport   string   `json:"transport"`
	Direct      bool     `json:"direct"`
	ObservedMS  int64    `json:"observed_ms"`
	ExpiresMS   int64    `json:"expires_ms"`
	Path        []string `json:"path"`
}

func printRoutes(raw any, w io.Writer) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var routes []routeDTO
	if err := json.Unmarshal(b, &routes); err != nil {
		return err
	}
	if len(routes) == 0 {
		fmt.Fprintln(w, "no routes")
		return nil
	}
	fmt.Fprintf(w, "%-16s  %-16s  %-8s  %s\n", "DESTINATION", "NEXT HOP", "COST", "HOPS")
	for _, r := range routes {
		fmt.Fprintf(w, "%-16s  %-16s  %-8s  %d\n", r.Destination, r.NextHop, fmt.Sprintf("%.1fms", r.CostMs), r.Hops)
	}
	return nil
}

func pingOverlay(ctx context.Context, ipText string, w io.Writer) error {
	addr, err := netip.ParseAddr(ipText)
	if err != nil {
		return fmt.Errorf("%q is not an IP address", ipText)
	}
	count := "-c"
	if runtime.GOOS == "windows" {
		count = "-n"
	}
	cmd := exec.CommandContext(ctx, "ping", count, "4", addr.String())
	cmd.Stdout = w
	cmd.Stderr = w
	return cmd.Run()
}
