package conf

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/identity"
)

func baseConfig(t *testing.T) Config {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Node: Node{
			OverlayIPv4Prefix: netip.MustParsePrefix("10.144.144.0/24"),
			OverlayIPv6Prefix: netip.MustParsePrefix("fd00:6d65:7368::/64"),
			OverlayIPv4:       netip.MustParseAddr("10.144.144.2"),
			OverlayIPv6:       netip.MustParseAddr("fd00:6d65:7368::2"),
			ListenPort:        11010,
			NetworkSecret:     bytes.Repeat([]byte{0x11}, secretSize),
			MTU:               1280,
			Interface:         "skein0",
		},
		API:       API{Listen: "127.0.0.1:11011", Token: bytes.Repeat([]byte{0x22}, secretSize)},
		Discovery: Discovery{MDNS: true},
		Peers: []Peer{{
			ID:        id.ID,
			PublicKey: id.PublicKey,
			Dialer:    true,
			Addrs:     []netip.AddrPort{netip.MustParseAddrPort("203.0.113.5:11010")},
		}},
	}
}

func TestValidateAcceptsBaseline(t *testing.T) {
	if err := Validate(baseConfig(t)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateAcceptsIPv4OnlyConfig(t *testing.T) {
	cfg := baseConfig(t)
	cfg.Node.OverlayIPv6 = netip.Addr{}
	cfg.Node.MTU = 1200
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate IPv4-only: %v", err)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	tests := map[string]func(*Config){
		"zero listen port":        func(c *Config) { c.Node.ListenPort = 0 },
		"mtu too small":           func(c *Config) { c.Node.MTU = MinMTU - 1 },
		"mtu too large":           func(c *Config) { c.Node.MTU = MaxMTU + 1 },
		"ipv6 overlay below 1280": func(c *Config) { c.Node.MTU = 1279 },
		"short psk":               func(c *Config) { c.Node.NetworkSecret = bytes.Repeat([]byte{1}, 31) },
		"long psk":                func(c *Config) { c.Node.NetworkSecret = bytes.Repeat([]byte{1}, 33) },
		"empty psk":               func(c *Config) { c.Node.NetworkSecret = nil },
		"short token":             func(c *Config) { c.API.Token = bytes.Repeat([]byte{1}, 16) },
		"empty token":             func(c *Config) { c.API.Token = nil },
		"non-loopback api":        func(c *Config) { c.API.Listen = "0.0.0.0:11011" },
		"hostname api":            func(c *Config) { c.API.Listen = "localhost:11011" },
		"default route prefix":    func(c *Config) { c.Node.OverlayIPv4Prefix = netip.MustParsePrefix("0.0.0.0/0") },
		"non-canonical prefix":    func(c *Config) { c.Node.OverlayIPv4Prefix = netip.MustParsePrefix("10.144.144.5/24") },
		"wrong family prefix":     func(c *Config) { c.Node.OverlayIPv4Prefix = netip.MustParsePrefix("fd00::/64") },
		"host outside prefix":     func(c *Config) { c.Node.OverlayIPv4 = netip.MustParseAddr("10.144.145.2") },
		"network address host":    func(c *Config) { c.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.0") },
		"broadcast host":          func(c *Config) { c.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.255") },
		"multicast host":          func(c *Config) { c.Node.OverlayIPv4 = netip.MustParseAddr("224.0.0.2") },
		"missing ipv4 host":       func(c *Config) { c.Node.OverlayIPv4 = netip.Addr{} },
		"bad interface name":      func(c *Config) { c.Node.Interface = "skein 0" },
		"long interface name":     func(c *Config) { c.Node.Interface = "skein0123456789a" },
		"zero peer id":            func(c *Config) { c.Peers[0].ID = 0 },
		"peer id mismatch":        func(c *Config) { c.Peers[0].ID++ },
		"duplicate peer":          func(c *Config) { c.Peers = append(c.Peers, c.Peers[0]) },
		"peer zero port":          func(c *Config) { c.Peers[0].Addrs = []netip.AddrPort{netip.MustParseAddrPort("203.0.113.5:0")} },
		"no peers and no mdns": func(c *Config) {
			c.Peers = nil
			c.Discovery.MDNS = false
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig(t)
			mutate(&cfg)
			if err := Validate(cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseAppliesDefaults(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc := `
[node]
overlay_ipv4 = "10.144.144.7"
network_secret = "qUKSgFNEmpqJiO3DS_vmRaYdxCAY_AZ_H6Cnmdn6E_U"

[api]
token = "4VFScqAStn6UYJtGDdyHG6SRYA9xCEHVANw2-iYqF1E"

[[peer]]
id = "` + hex16(id.ID) + `"
pubkey = "` + b64(id.PublicKey) + `"
addrs = ["udp://203.0.113.9:11010"]
`
	cfg, err := Parse(doc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Node.ListenPort != DefaultListenPort || cfg.Node.MTU != DefaultMTU {
		t.Fatalf("defaults not applied: port=%d mtu=%d", cfg.Node.ListenPort, cfg.Node.MTU)
	}
	if cfg.Node.OverlayIPv4Prefix.String() != defaultIPv4Prefix || cfg.Node.OverlayIPv6Prefix.String() != defaultIPv6Prefix {
		t.Fatalf("prefix defaults not applied: %v %v", cfg.Node.OverlayIPv4Prefix, cfg.Node.OverlayIPv6Prefix)
	}
	if cfg.API.Listen != DefaultAPIBind || !cfg.Discovery.MDNS {
		t.Fatalf("api/discovery defaults not applied: %+v %+v", cfg.API, cfg.Discovery)
	}
	if cfg.Node.Interface != DefaultInterface() {
		t.Fatalf("interface = %q, want %q", cfg.Node.Interface, DefaultInterface())
	}
	if cfg.Peers[0].Dialer {
		t.Fatal("dialer must default to false")
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	_, err := Parse("[node]\noverlay_ipv44 = \"10.0.0.1\"\n")
	if err == nil || !strings.Contains(err.Error(), "unknown configuration key") {
		t.Fatalf("err = %v, want unknown key error", err)
	}
}

func TestLoadFixtures(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "testdata", "config", "valid.toml"))
	if err != nil {
		t.Fatalf("Load valid: %v", err)
	}
	if cfg.Node.ListenPort != 11010 || len(cfg.Peers) != 1 || !cfg.Peers[0].Dialer {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}
	if _, err := Load(filepath.Join("..", "..", "testdata", "config", "invalid-prefix.toml")); err == nil {
		t.Fatal("expected invalid-prefix.toml to fail validation")
	}
}

func TestValidateConfigDirRejectsDoubledAppData(t *testing.T) {
	for _, dir := range []string{
		`C:\Users\a\AppData\Roaming`,
		`/home/a/.config`,
		`C:\Users\a\AppData\Roaming\skein`,
	} {
		if err := validateConfigDir(dir); err != nil {
			t.Fatalf("validateConfigDir(%q): %v", dir, err)
		}
	}
	for _, dir := range []string{
		`C:\Users\a\AppData\Roaming:C:\Users\a\AppData\Roaming`,
		`C:\Users\a\AppData\Roaming:C:\Users\a\AppData\Roaming\skein`,
	} {
		if err := validateConfigDir(dir); err == nil {
			t.Fatalf("expected %q to be rejected", dir)
		}
	}
}

func TestCheckPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "skein.toml")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPermissions(path); err != nil {
		t.Fatalf("0600 config rejected: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPermissions(path); err == nil {
		t.Fatal("expected world-readable config to be rejected")
	}
}

func hex16(v uint64) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := range 16 {
		out[15-i] = digits[v&0xf]
		v >>= 4
	}
	return string(out)
}

func b64(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		rem := len(b) - i
		n = uint32(b[i]) << 16
		if rem > 1 {
			n |= uint32(b[i+1]) << 8
		}
		if rem > 2 {
			n |= uint32(b[i+2])
		}
		sb.WriteByte(alphabet[n>>18&0x3f])
		sb.WriteByte(alphabet[n>>12&0x3f])
		if rem > 1 {
			sb.WriteByte(alphabet[n>>6&0x3f])
		} else {
			sb.WriteByte('=')
		}
		if rem > 2 {
			sb.WriteByte(alphabet[n&0x3f])
		} else {
			sb.WriteByte('=')
		}
	}
	return sb.String()
}

func TestParseRejectsPortAndMTUBounds(t *testing.T) {
	cases := map[string]string{
		"port zero":     "[node]\nlisten_port = 0\n",
		"port too high": "[node]\nlisten_port = 65536\n",
		"negative port": "[node]\nlisten_port = -1\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(doc); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
	doc := "[node]\nmtu = 100\n"
	if _, err := Parse(doc); err == nil {
		t.Fatal("expected MTU below minimum to be rejected")
	}
}

func TestNodeNameValidation(t *testing.T) {
	cfg := baseConfig(t)
	cfg.Node.Name = "utsav-laptop"
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
	cfg.Node.Name = " leading"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected leading whitespace to be rejected")
	}
	cfg.Node.Name = strings.Repeat("x", 33)
	if err := Validate(cfg); err == nil {
		t.Fatal("expected an over-long name to be rejected")
	}
	cfg.Node.Name = "bad\x01name"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected a control character to be rejected")
	}
}

func TestRenderRoundTrips(t *testing.T) {
	cfg := baseConfig(t)
	text, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := Parse(string(text))
	if err != nil {
		t.Fatalf("Parse(Render()): %v\n%s", err, text)
	}
	if got.Node.OverlayIPv4 != cfg.Node.OverlayIPv4 || got.Node.ListenPort != cfg.Node.ListenPort {
		t.Fatalf("node mismatch: %+v", got.Node)
	}
	if !bytes.Equal(got.Node.NetworkSecret, cfg.Node.NetworkSecret) || !bytes.Equal(got.API.Token, cfg.API.Token) {
		t.Fatal("secrets did not round-trip")
	}
	if len(got.Peers) != len(cfg.Peers) || got.Peers[0].ID != cfg.Peers[0].ID || !got.Peers[0].Dialer {
		t.Fatalf("peers = %+v", got.Peers)
	}
	if len(got.Peers[0].Addrs) != 1 || got.Peers[0].Addrs[0] != cfg.Peers[0].Addrs[0] {
		t.Fatalf("peer addrs = %v", got.Peers[0].Addrs)
	}
}

func TestNextFreeOverlayHashedSpreadsNodes(t *testing.T) {
	prefix := netip.MustParsePrefix("10.144.144.0/24")
	used := []netip.Addr{netip.MustParseAddr("10.144.144.1")}
	a, err := NextFreeOverlayHashed(prefix, used, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NextFreeOverlayHashed(prefix, used, 2)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("different seeds produced the same address %s", a)
	}
	for _, addr := range []netip.Addr{a, b} {
		if addr == netip.MustParseAddr("10.144.144.1") || !prefix.Contains(addr) {
			t.Fatalf("bad allocation %s", addr)
		}
	}
}

func TestNextFreeOverlay(t *testing.T) {
	prefix := netip.MustParsePrefix("10.144.144.0/24")
	used := []netip.Addr{
		netip.MustParseAddr("10.144.144.1"),
		netip.MustParseAddr("10.144.144.2"),
	}
	addr, err := NextFreeOverlay(prefix, used)
	if err != nil {
		t.Fatal(err)
	}
	if addr.String() != "10.144.144.3" {
		t.Fatalf("NextFreeOverlay = %s, want 10.144.144.3", addr)
	}
	if _, err := NextFreeOverlay(netip.MustParsePrefix("10.144.144.0/30"), []netip.Addr{
		netip.MustParseAddr("10.144.144.1"),
		netip.MustParseAddr("10.144.144.2"),
	}); err == nil {
		t.Fatal("expected a /30 with two hosts to be exhausted")
	}
}

func TestPublicAddrRoundTripsAndValidates(t *testing.T) {
	cfg := baseConfig(t)
	cfg.Node.PublicAddr = netip.MustParseAddrPort("203.0.113.7:11010")
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid public_addr rejected: %v", err)
	}
	text, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), `public_addr`) {
		t.Fatalf("public_addr missing from render:\n%s", text)
	}
	parsed, err := Parse(string(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Node.PublicAddr != cfg.Node.PublicAddr {
		t.Fatalf("public_addr = %s, want %s", parsed.Node.PublicAddr, cfg.Node.PublicAddr)
	}

	cfg.Node.PublicAddr = netip.MustParseAddrPort("203.0.113.7:0")
	if err := Validate(cfg); err == nil {
		t.Fatal("expected a zero port to be rejected")
	}
	cfg.Node.PublicAddr = netip.MustParseAddrPort("10.144.144.9:11010")
	if err := Validate(cfg); err == nil {
		t.Fatal("expected an overlay address to be rejected")
	}
}

func TestValidateRejectsOverlayEndpoint(t *testing.T) {
	cfg := Default()
	ident, _ := identity.Generate()
	cfg.Peers = []Peer{{ID: ident.ID, PublicKey: ident.PublicKey, Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.144.144.9:11010")}}}
	if err := Validate(cfg); err == nil {
		t.Fatal("endpoint inside the overlay prefix was accepted")
	}
}

func TestValidateRejectsLoopbackPrefix(t *testing.T) {
	cfg := Default()
	cfg.Node.OverlayIPv4Prefix = netip.MustParsePrefix("127.0.0.0/8")
	cfg.Node.OverlayIPv4 = netip.MustParseAddr("127.0.0.2")
	if err := Validate(cfg); err == nil {
		t.Fatal("loopback overlay prefix was accepted")
	}
}

func TestValidateRejectsOptionLikeInterface(t *testing.T) {
	cfg := Default()
	cfg.Node.Interface = "-skein"
	if err := Validate(cfg); err == nil {
		t.Fatal("interface starting with '-' was accepted")
	}
}

func TestNextFreeOverlayRejectsIPv6(t *testing.T) {
	if _, err := NextFreeOverlay(netip.MustParsePrefix("fd00::/64"), nil); err == nil {
		t.Fatal("IPv6 overlay prefix was accepted by the IPv4 allocator")
	}
}
