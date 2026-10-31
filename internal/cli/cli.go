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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Utsav-X-Bit/skein/internal/conf"
	"github.com/Utsav-X-Bit/skein/internal/identity"
	"github.com/Utsav-X-Bit/skein/internal/path"
	"github.com/Utsav-X-Bit/skein/internal/version"
)

const usage = `skein — Skein mesh VPN command line

Usage:
  skein [-c <config>] init        create identity, config, PSK, and API token
  skein [-c <config>] genkey      print the public key and node ID
  skein [-c <config>] invite      print this node's invite string
  skein [-c <config>] status [--json]
  skein [-c <config>] peers [list|add <invite>|rm <peer-id>] [--json]
  skein [-c <config>] routes [--json]
  skein [-c <config>] probe <peer-id>
  skein [-c <config>] ping <overlay-ip>
  skein version
`

// Run executes one CLI invocation and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	path, rest, jsonOut, err := parseGlobal(args)
	if err != nil {
		fmt.Fprintf(stderr, "skein: %v\n\n%s", err, usage)
		return 2
	}
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
		if err := cmdInvite(path, stdout); err != nil {
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

func parseGlobal(args []string) (path string, rest []string, jsonOut bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c", "--config":
			if i+1 >= len(args) {
				return "", nil, false, fmt.Errorf("%s requires a path", args[i])
			}
			path = args[i+1]
			i++
		case "--json":
			jsonOut = true
		default:
			rest = append(rest, args[i])
		}
	}
	if path == "" {
		path, err = conf.DefaultPath()
	}
	return path, rest, jsonOut, err
}

func loadConfig(path string) (conf.Config, error) {
	cfg, err := conf.Load(path)
	if err != nil {
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
	cfgText := fmt.Sprintf(`# Skein configuration. Edit overlay addresses before starting.
[node]
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
`, base64.RawURLEncoding.EncodeToString(psk), conf.DefaultInterface(), base64.RawURLEncoding.EncodeToString(token))

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

	cands, _ := path.LocalCandidates(conf.DefaultListenPort)
	inv := identity.Invite{ID: id.ID, PublicKey: id.PublicKey, Addrs: cands}
	invite, _ := identity.EncodeInvite(inv)

	fmt.Fprintf(stdout, "config:     %s\n", configPath)
	fmt.Fprintf(stdout, "identity:   %s\n", keyPath)
	fmt.Fprintf(stdout, "node id:    %016x\n", id.ID)
	fmt.Fprintf(stdout, "public key: %s\n", base64.StdEncoding.EncodeToString(id.PublicKey))
	fmt.Fprintf(stdout, "invite:     %s\n", invite)
	fmt.Fprintf(stdout, "network secret (share out of band with every node): %s\n", base64.RawURLEncoding.EncodeToString(psk))
	fmt.Fprintln(stdout, "edit overlay_ipv4 in the config so every node has a unique address")
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

func cmdInvite(configPath string, stdout io.Writer) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	id, err := identity.Load(identityPath(configPath))
	if err != nil {
		return err
	}
	cands, err := path.LocalCandidates(cfg.Node.ListenPort)
	if err != nil {
		return err
	}
	invite, err := identity.EncodeInvite(identity.Invite{ID: id.ID, PublicKey: id.PublicKey, Addrs: cands})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, invite)
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
		return fmt.Errorf("contact skeind: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("skeind: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
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
		return fmt.Errorf("contact skeind: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("skeind: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
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
	ID          string  `json:"id"`
	State       string  `json:"state"`
	Path        string  `json:"path"`
	RTTMs       float64 `json:"rtt_ms"`
	OverlayIPv4 string  `json:"overlay_ipv4"`
	Endpoint    string  `json:"endpoint"`
	Direct      bool    `json:"direct"`
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
	fmt.Fprintf(w, "%-16s  %-6s  %-4s  %-10s  %-6s  %s\n", "ID", "STATE", "PATH", "RTT", "DIRECT", "ENDPOINT")
	for _, p := range peers {
		fmt.Fprintf(w, "%-16s  %-6s  %-4s  %-10s  %-6v  %s\n",
			p.ID, p.State, p.Path, fmt.Sprintf("%.1fms", p.RTTMs), p.Direct, p.Endpoint)
	}
	return nil
}

type routeDTO struct {
	Destination string  `json:"destination"`
	NextHop     string  `json:"next_hop"`
	CostMs      float64 `json:"cost_ms"`
	Hops        uint8   `json:"hops"`
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
