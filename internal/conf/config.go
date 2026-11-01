// Package conf parses and validates the Skein TOML configuration.
package conf

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Utsav-X-bit/skein/internal/identity"
)

const (
	// DefaultListenPort is the default UDP port for the underlay.
	DefaultListenPort = 11010
	// DefaultMTU is the default inner TUN MTU.
	DefaultMTU = 1280
	// MinMTU is the smallest inner MTU accepted.
	MinMTU = 576
	// MaxMTU bounds the inner MTU well below the UDP datagram limit.
	MaxMTU = 9000
	// DefaultAPIBind is the only allowed API listen address family and port.
	DefaultAPIBind = "127.0.0.1:11011"

	defaultIPv4Prefix = "10.144.144.0/24"
	defaultIPv6Prefix = "fd00:6d65:7368::/64"
	secretSize        = 32
)

// Config is the validated runtime configuration.
type Config struct {
	Node      Node
	API       API
	Discovery Discovery
	Peers     []Peer
	// Path is the file the configuration was loaded from; empty for in-memory configs.
	Path string
}

// Node holds overlay addressing and the underlay socket settings.
type Node struct {
	OverlayIPv4Prefix netip.Prefix
	OverlayIPv6Prefix netip.Prefix
	OverlayIPv4       netip.Addr
	OverlayIPv6       netip.Addr
	ListenPort        uint16
	NetworkSecret     []byte
	MTU               int
	Interface         string
	Name              string
}

// API describes the loopback control API.
type API struct {
	Listen string
	Token  []byte
}

// Discovery toggles zero-configuration LAN discovery.
type Discovery struct {
	MDNS bool
}

// Peer is a statically configured remote node.
type Peer struct {
	ID        uint64
	Name      string
	PublicKey []byte
	Addrs     []netip.AddrPort
	// Dialer reports whether this node initiates the session to the peer.
	Dialer bool
}

type fileConfig struct {
	Node struct {
		OverlayIPv4Prefix string `toml:"overlay_ipv4_prefix"`
		OverlayIPv6Prefix string `toml:"overlay_ipv6_prefix"`
		OverlayIPv4       string `toml:"overlay_ipv4"`
		OverlayIPv6       string `toml:"overlay_ipv6"`
		ListenPort        *int   `toml:"listen_port"`
		NetworkSecret     string `toml:"network_secret"`
		MTU               *int   `toml:"mtu"`
		Interface         string `toml:"interface"`
		Name              string `toml:"name"`
	} `toml:"node"`
	API struct {
		Listen string `toml:"listen"`
		Token  string `toml:"token"`
	} `toml:"api"`
	Discovery struct {
		MDNS *bool `toml:"mdns"`
	} `toml:"discovery"`
	Peer []struct {
		ID     string   `toml:"id"`
		Name   string   `toml:"name"`
		PubKey string   `toml:"pubkey"`
		Dialer bool     `toml:"dialer"`
		Addrs  []string `toml:"addrs"`
	} `toml:"peer"`
}

// DefaultInterface returns the default TUN interface name for the current OS.
func DefaultInterface() string {
	if runtime.GOOS == "windows" {
		return "Skein"
	}
	return "skein0"
}

// Default returns the built-in defaults, before validation.
func Default() Config {
	return Config{
		Node: Node{
			OverlayIPv4Prefix: netip.MustParsePrefix(defaultIPv4Prefix),
			OverlayIPv6Prefix: netip.MustParsePrefix(defaultIPv6Prefix),
			ListenPort:        DefaultListenPort,
			MTU:               DefaultMTU,
			Interface:         DefaultInterface(),
		},
		API:       API{Listen: DefaultAPIBind},
		Discovery: Discovery{MDNS: true},
	}
}

// NextFreeOverlay returns the lowest usable host address in prefix that is not
// already taken.
func NextFreeOverlay(prefix netip.Prefix, used []netip.Addr) (netip.Addr, error) {
	if !prefix.IsValid() || prefix.Bits() == 0 {
		return netip.Addr{}, errors.New("conf: invalid overlay prefix")
	}
	taken := make(map[netip.Addr]struct{}, len(used))
	for _, addr := range used {
		taken[addr] = struct{}{}
	}
	last := broadcast(prefix)
	addr := prefix.Masked().Addr()
	for range 4096 {
		addr = addr.Next()
		if !addr.IsValid() || !prefix.Contains(addr) {
			break
		}
		if addr == last {
			continue
		}
		if _, exists := taken[addr]; exists {
			continue
		}
		return addr, nil
	}
	return netip.Addr{}, fmt.Errorf("conf: %s has no free host address", prefix)
}

// Render serializes cfg as TOML in the same schema Load parses.
func Render(cfg Config) ([]byte, error) {
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("[node]\n")
	fmt.Fprintf(&b, "overlay_ipv4_prefix = %q\n", cfg.Node.OverlayIPv4Prefix.String())
	fmt.Fprintf(&b, "overlay_ipv6_prefix = %q\n", cfg.Node.OverlayIPv6Prefix.String())
	fmt.Fprintf(&b, "overlay_ipv4        = %q\n", cfg.Node.OverlayIPv4.String())
	if cfg.Node.OverlayIPv6.IsValid() {
		fmt.Fprintf(&b, "overlay_ipv6        = %q\n", cfg.Node.OverlayIPv6.String())
	}
	fmt.Fprintf(&b, "listen_port         = %d\n", cfg.Node.ListenPort)
	fmt.Fprintf(&b, "network_secret      = %q\n", base64.RawURLEncoding.EncodeToString(cfg.Node.NetworkSecret))
	fmt.Fprintf(&b, "mtu                 = %d\n", cfg.Node.MTU)
	fmt.Fprintf(&b, "interface           = %q\n", cfg.Node.Interface)
	if cfg.Node.Name != "" {
		fmt.Fprintf(&b, "name                = %q\n", cfg.Node.Name)
	}
	b.WriteString("\n[api]\n")
	fmt.Fprintf(&b, "listen = %q\n", cfg.API.Listen)
	fmt.Fprintf(&b, "token  = %q\n", base64.RawURLEncoding.EncodeToString(cfg.API.Token))
	b.WriteString("\n[discovery]\n")
	fmt.Fprintf(&b, "mdns = %v\n", cfg.Discovery.MDNS)
	for _, p := range cfg.Peers {
		b.WriteString("\n[[peer]]\n")
		fmt.Fprintf(&b, "id     = %q\n", formatID(p.ID))
		if p.Name != "" {
			fmt.Fprintf(&b, "name   = %q\n", p.Name)
		}
		fmt.Fprintf(&b, "pubkey = %q\n", base64.StdEncoding.EncodeToString(p.PublicKey))
		fmt.Fprintf(&b, "dialer = %v\n", p.Dialer)
		if len(p.Addrs) == 0 {
			b.WriteString("addrs  = []\n")
			continue
		}
		b.WriteString("addrs  = [")
		for i, addr := range p.Addrs {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", "udp://"+addr.String())
		}
		b.WriteString("]\n")
	}
	return []byte(b.String()), nil
}

func formatID(id uint64) string {
	return fmt.Sprintf("%016x", id)
}

// DefaultPathForUser returns the configuration path of the invoking user. When
// the command runs through sudo, that is the sudo user's config, not root's.
func DefaultPathForUser() (string, error) {
	if elevated() {
		if name := os.Getenv("SUDO_USER"); name != "" && name != "root" {
			if u, err := user.Lookup(name); err == nil && u.HomeDir != "" {
				return filepath.Join(u.HomeDir, ".config", "skein", "skein.toml"), nil
			}
		}
	}
	return DefaultPath()
}

// DefaultPath returns the per-user configuration file path.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if err := validateConfigDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "skein", "skein.toml"), nil
}

// validateConfigDir rejects a malformed config directory. The common cause is
// APPDATA being concatenated with itself, which yields "C:\a:C:\a".
func validateConfigDir(dir string) error {
	if len(dir) > 2 && dir[1] == ':' && strings.Contains(dir[2:], ":") {
		return fmt.Errorf("config directory %q is malformed; check the APPDATA environment variable", dir)
	}
	return nil
}

// Load parses path, applies defaults, and validates the result.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg, err := parse(string(raw))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	return cfg, nil
}

// Parse parses a TOML document, applies defaults, and validates the result.
func Parse(text string) (Config, error) { return parse(text) }

func parse(text string) (Config, error) {
	var fc fileConfig
	md, err := toml.Decode(text, &fc)
	if err != nil {
		return Config{}, err
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("unknown configuration key %q", undecoded[0].String())
	}

	cfg := Default()

	if s := fc.Node.OverlayIPv4Prefix; s != "" {
		if cfg.Node.OverlayIPv4Prefix, err = netip.ParsePrefix(s); err != nil {
			return Config{}, fmt.Errorf("node.overlay_ipv4_prefix: %w", err)
		}
	}
	if s := fc.Node.OverlayIPv6Prefix; s != "" {
		if cfg.Node.OverlayIPv6Prefix, err = netip.ParsePrefix(s); err != nil {
			return Config{}, fmt.Errorf("node.overlay_ipv6_prefix: %w", err)
		}
	}
	if s := fc.Node.OverlayIPv4; s != "" {
		if cfg.Node.OverlayIPv4, err = netip.ParseAddr(s); err != nil {
			return Config{}, fmt.Errorf("node.overlay_ipv4: %w", err)
		}
	}
	if s := fc.Node.OverlayIPv6; s != "" {
		if cfg.Node.OverlayIPv6, err = netip.ParseAddr(s); err != nil {
			return Config{}, fmt.Errorf("node.overlay_ipv6: %w", err)
		}
	}
	if fc.Node.ListenPort != nil {
		if *fc.Node.ListenPort < 1 || *fc.Node.ListenPort > 65535 {
			return Config{}, fmt.Errorf("node.listen_port must be between 1 and 65535, got %d", *fc.Node.ListenPort)
		}
		cfg.Node.ListenPort = uint16(*fc.Node.ListenPort)
	}
	if fc.Node.MTU != nil {
		cfg.Node.MTU = *fc.Node.MTU
	}
	if s := fc.Node.Interface; s != "" {
		cfg.Node.Interface = s
	}
	if s := fc.Node.Name; s != "" {
		cfg.Node.Name = s
	}
	if s := fc.Node.NetworkSecret; s != "" {
		if cfg.Node.NetworkSecret, err = decodeSecret("node.network_secret", s); err != nil {
			return Config{}, err
		}
	}

	if s := fc.API.Listen; s != "" {
		cfg.API.Listen = s
	}
	if s := fc.API.Token; s != "" {
		if cfg.API.Token, err = decodeSecret("api.token", s); err != nil {
			return Config{}, err
		}
	}
	if fc.Discovery.MDNS != nil {
		cfg.Discovery.MDNS = *fc.Discovery.MDNS
	}

	for i, fp := range fc.Peer {
		peer, err := parsePeer(fp.ID, fp.Name, fp.PubKey, fp.Dialer, fp.Addrs)
		if err != nil {
			return Config{}, fmt.Errorf("peer %d: %w", i, err)
		}
		cfg.Peers = append(cfg.Peers, peer)
	}

	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parsePeer(idText, name, pubText string, dialer bool, addrText []string) (Peer, error) {
	if idText == "" {
		return Peer{}, errors.New("id is required")
	}
	id, err := strconv.ParseUint(idText, 16, 64)
	if err != nil {
		return Peer{}, fmt.Errorf("id %q is not 16 hexadecimal digits", idText)
	}
	if id == 0 {
		return Peer{}, errors.New("id must not be zero")
	}
	pub, err := decodeKey("pubkey", pubText)
	if err != nil {
		return Peer{}, err
	}
	derived, err := identity.PeerID(pub)
	if err != nil {
		return Peer{}, err
	}
	if derived != id {
		return Peer{}, fmt.Errorf("id %016x does not match pubkey (%016x)", id, derived)
	}
	if err := validateName(name); err != nil {
		return Peer{}, err
	}
	peer := Peer{ID: id, Name: name, PublicKey: pub, Dialer: dialer}
	for _, text := range addrText {
		addr, err := parseEndpoint(text)
		if err != nil {
			return Peer{}, err
		}
		peer.Addrs = append(peer.Addrs, addr)
	}
	return peer, nil
}

func decodeKey(field, s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("%s is required", field)
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err := enc.DecodeString(s); err == nil {
			if len(raw) != identity.KeySize {
				return nil, fmt.Errorf("%s must be %d bytes, got %d", field, identity.KeySize, len(raw))
			}
			return raw, nil
		}
	}
	return nil, fmt.Errorf("%s is not valid base64", field)
}

func decodeSecret(field, s string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64url: %w", field, err)
	}
	if len(raw) != secretSize {
		return nil, fmt.Errorf("%s must be exactly %d bytes, got %d", field, secretSize, len(raw))
	}
	return raw, nil
}

func parseEndpoint(s string) (netip.AddrPort, error) {
	u, err := url.Parse(s)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("endpoint %q: %w", s, err)
	}
	if u.Scheme != "udp" {
		return netip.AddrPort{}, fmt.Errorf("endpoint %q must use the udp:// scheme", s)
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return netip.AddrPort{}, fmt.Errorf("endpoint %q must be udp://<ip>:<port>", s)
	}
	addr, err := netip.ParseAddrPort(u.Host)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("endpoint %q: %w", s, err)
	}
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	if addr.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("endpoint %q has port zero", s)
	}
	if addr.Addr().IsUnspecified() || addr.Addr().IsMulticast() {
		return netip.AddrPort{}, fmt.Errorf("endpoint %q is not a unicast address", s)
	}
	return addr, nil
}

// Validate checks a configuration for internal consistency and safety.
func Validate(cfg Config) error {
	n := cfg.Node

	if n.ListenPort == 0 {
		return errors.New("node.listen_port must be between 1 and 65535")
	}
	if n.MTU < MinMTU || n.MTU > MaxMTU {
		return fmt.Errorf("node.mtu must be between %d and %d, got %d", MinMTU, MaxMTU, n.MTU)
	}
	if len(n.NetworkSecret) != secretSize {
		return fmt.Errorf("node.network_secret must be exactly %d bytes, got %d", secretSize, len(n.NetworkSecret))
	}
	if err := validateInterface(n.Interface); err != nil {
		return err
	}
	if err := validateName(n.Name); err != nil {
		return err
	}

	if err := validatePrefix("node.overlay_ipv4_prefix", n.OverlayIPv4Prefix, true); err != nil {
		return err
	}
	if err := validatePrefix("node.overlay_ipv6_prefix", n.OverlayIPv6Prefix, false); err != nil {
		return err
	}
	if n.OverlayIPv4Prefix.Overlaps(n.OverlayIPv6Prefix) {
		return errors.New("overlay IPv4 and IPv6 prefixes must not overlap")
	}

	if !n.OverlayIPv4.IsValid() {
		return errors.New("node.overlay_ipv4 is required")
	}
	if err := validateHost("node.overlay_ipv4", n.OverlayIPv4, n.OverlayIPv4Prefix); err != nil {
		return err
	}
	if n.OverlayIPv6.IsValid() {
		if err := validateHost("node.overlay_ipv6", n.OverlayIPv6, n.OverlayIPv6Prefix); err != nil {
			return err
		}
		if n.MTU < 1280 {
			return fmt.Errorf("node.mtu %d is too small for an IPv6 overlay; minimum is 1280", n.MTU)
		}
	}

	apiAddr, err := netip.ParseAddrPort(cfg.API.Listen)
	if err != nil {
		return fmt.Errorf("api.listen: %w", err)
	}
	if !apiAddr.Addr().IsLoopback() || apiAddr.Port() == 0 {
		return fmt.Errorf("api.listen must be a loopback IP and port, got %s", cfg.API.Listen)
	}
	if len(cfg.API.Token) != secretSize {
		return fmt.Errorf("api.token must be exactly %d bytes, got %d", secretSize, len(cfg.API.Token))
	}

	if len(cfg.Peers) == 0 && !cfg.Discovery.MDNS {
		return errors.New("at least one peer is required when discovery.mdns is disabled")
	}
	seen := make(map[uint64]struct{}, len(cfg.Peers))
	for i, p := range cfg.Peers {
		if p.ID == 0 {
			return fmt.Errorf("peer %d: id must not be zero", i)
		}
		if len(p.PublicKey) != identity.KeySize {
			return fmt.Errorf("peer %d: pubkey must be %d bytes", i, identity.KeySize)
		}
		derived, err := identity.PeerID(p.PublicKey)
		if err != nil {
			return fmt.Errorf("peer %d: %w", i, err)
		}
		if derived != p.ID {
			return fmt.Errorf("peer %d: id %016x does not match pubkey (%016x)", i, p.ID, derived)
		}
		if _, dup := seen[p.ID]; dup {
			return fmt.Errorf("peer %016x is configured twice", p.ID)
		}
		seen[p.ID] = struct{}{}
		if len(p.Addrs) > identity.MaxEndpoints {
			return fmt.Errorf("peer %016x carries %d endpoints, maximum is %d", p.ID, len(p.Addrs), identity.MaxEndpoints)
		}
		for _, a := range p.Addrs {
			if !a.IsValid() || a.Port() == 0 || a.Addr().IsUnspecified() || a.Addr().IsMulticast() {
				return fmt.Errorf("peer %016x has invalid endpoint %s", p.ID, a)
			}
		}
	}
	return nil
}

func validatePrefix(field string, p netip.Prefix, want4 bool) error {
	if !p.IsValid() {
		return fmt.Errorf("%s is not a valid prefix", field)
	}
	if p != p.Masked() {
		return fmt.Errorf("%s must be a canonical prefix, got %s (use %s)", field, p, p.Masked())
	}
	if p.Addr().Is4() != want4 {
		return fmt.Errorf("%s has the wrong address family", field)
	}
	if p.Bits() == 0 {
		return fmt.Errorf("%s must not be a default route", field)
	}
	if p.Addr().IsUnspecified() {
		return fmt.Errorf("%s must not start at the unspecified address", field)
	}
	return nil
}

func validateHost(field string, addr netip.Addr, prefix netip.Prefix) error {
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() {
		return fmt.Errorf("%s must be a unicast overlay address, got %s", field, addr)
	}
	if !prefix.Contains(addr) {
		return fmt.Errorf("%s %s is outside %s", field, addr, prefix)
	}
	if addr == prefix.Addr() {
		return fmt.Errorf("%s must not be the network address of %s", field, prefix)
	}
	if addr.Is4() && addr == broadcast(prefix) {
		return fmt.Errorf("%s must not be the broadcast address of %s", field, prefix)
	}
	return nil
}

func broadcast(p netip.Prefix) netip.Addr {
	a := p.Addr().As4()
	bits := p.Bits()
	for i := range 4 {
		switch networkBits := bits - 8*i; {
		case networkBits >= 8:
			// Fully inside the network part; keep the network address octet.
		case networkBits <= 0:
			a[i] = 0xff
		default:
			a[i] |= 0xff >> networkBits
		}
	}
	return netip.AddrFrom4(a)
}

// validateName bounds the optional human-readable node name.
func validateName(name string) error {
	if name == "" {
		return nil
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("node.name must not start or end with whitespace")
	}
	if len([]rune(name)) > 32 {
		return fmt.Errorf("node.name must be at most 32 characters, got %d", len([]rune(name)))
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("node.name contains a control character")
		}
	}
	return nil
}

func validateInterface(name string) error {
	if name == "" || len(name) > 15 {
		return fmt.Errorf("node.interface must be 1-15 characters, got %q", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("node.interface must not be %q", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("node.interface contains invalid character %q", r)
		}
	}
	return nil
}

// CheckPermissions rejects a secret-bearing configuration readable by group or others.
func CheckPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%s is accessible by group or others (mode %o); run: chmod 600 %s", path, mode, path)
	}
	return nil
}
