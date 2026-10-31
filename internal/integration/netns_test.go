package integration

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/utsavgupta5467/skein/internal/identity"
)

// TestNetns runs the dual-stack bridge proof inside Linux network namespaces.
// It requires root and the topology created by test/netns/setup.sh.
func TestNetns(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("netns test requires root; run sudo ./test/netns/run.sh")
	}
	prefix := os.Getenv("SKEIN_NETNS_PREFIX")
	if prefix == "" {
		t.Skip("SKEIN_NETNS_PREFIX is not set; run sudo ./test/netns/run.sh")
	}
	ns := map[string]string{
		"n4":    prefix + "-n4",
		"n6":    prefix + "-n6",
		"ndual": prefix + "-ndual",
	}
	for name, namespace := range ns {
		if out, err := exec.Command("ip", "netns", "list").CombinedOutput(); err != nil || !containsLine(string(out), namespace) {
			t.Fatalf("namespace %s is missing: %v %s", name, err, out)
		}
	}

	binDir := t.TempDir()
	build(t, filepath.Join(binDir, "skeind"), "./cmd/skeind")
	build(t, filepath.Join(binDir, "skein"), "./cmd/skein")

	cfgDir := t.TempDir()
	psk := bytes.Repeat([]byte{0x5a}, identity.KeySize)
	token := bytes.Repeat([]byte{0x3c}, identity.KeySize)

	type nodeCfg struct {
		name      string
		namespace string
		overlay   string
		peers     []peerSpec
	}
	nodes := []nodeCfg{
		{
			name: "n4", namespace: ns["n4"], overlay: "10.144.144.1",
			peers: []peerSpec{
				{name: "ndual", dialer: true, addr: "192.168.10.2:11010"},
				{name: "n6", dialer: false, addr: "[fd00:10::1]:11010"},
			},
		},
		{
			name: "n6", namespace: ns["n6"], overlay: "10.144.144.3",
			peers: []peerSpec{
				{name: "ndual", dialer: true, addr: "[fd00:10::2]:11010"},
				{name: "n4", dialer: false, addr: "192.168.10.1:11010"},
			},
		},
		{
			name: "ndual", namespace: ns["ndual"], overlay: "10.144.144.2",
			peers: []peerSpec{
				{name: "n4", dialer: false, addr: "192.168.10.1:11010"},
				{name: "n6", dialer: false, addr: "[fd00:10::1]:11010"},
			},
		},
	}

	ids := map[string]identity.Identity{}
	paths := map[string]string{}
	for _, n := range nodes {
		id, err := identity.Generate()
		if err != nil {
			t.Fatal(err)
		}
		ids[n.name] = id
		dir := filepath.Join(cfgDir, n.name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := identity.Save(filepath.Join(dir, "identity.key"), id); err != nil {
			t.Fatal(err)
		}
		paths[n.name] = filepath.Join(dir, "skein.toml")
	}

	for _, n := range nodes {
		var peerText strings.Builder
		for _, p := range n.peers {
			peerText.WriteString(fmt.Sprintf(`
[[peer]]
id     = "%016x"
pubkey = "%s"
dialer = %v
addrs  = ["udp://%s"]
`, ids[p.name].ID, base64.StdEncoding.EncodeToString(ids[p.name].PublicKey), p.dialer, p.addr))
		}
		cfg := fmt.Sprintf(`[node]
overlay_ipv4_prefix = "10.144.144.0/24"
overlay_ipv6_prefix = "fd00:6d65:7368::/64"
overlay_ipv4        = "%s"
listen_port         = 11010
network_secret      = "%s"
mtu                 = 1280
interface           = "skein0"

[api]
listen = "127.0.0.1:11011"
token  = "%s"

[discovery]
mdns = false
%s`, n.overlay,
			base64.RawURLEncoding.EncodeToString(psk),
			base64.RawURLEncoding.EncodeToString(token),
			peerText.String())
		if err := os.WriteFile(paths[n.name], []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	procs := make([]*exec.Cmd, 0, len(nodes))
	defer func() {
		for _, p := range procs {
			if p.Process != nil {
				_ = p.Process.Signal(syscall.SIGTERM)
			}
		}
		for _, p := range procs {
			_ = p.Wait()
		}
	}()

	for _, n := range nodes {
		cmd := exec.Command("ip", "netns", "exec", n.namespace,
			filepath.Join(binDir, "skeind"), "run", "-c", paths[n.name])
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %s: %v", n.name, err)
		}
		procs = append(procs, cmd)
	}

	// Every daemon must report both bridge links up.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if peersUp(t, ns["ndual"], filepath.Join(binDir, "skein"), paths["ndual"], 2) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !peersUp(t, ns["ndual"], filepath.Join(binDir, "skein"), paths["ndual"], 2) {
		t.Fatal("ndual did not establish both direct links")
	}

	// n4 must reach the IPv6-only node's overlay address through ndual.
	waitFor(t, 20*time.Second, func() bool {
		out := run(t, "ip", "netns", "exec", ns["n4"], filepath.Join(binDir, "skein"), "-c", paths["n4"], "routes", "--json")
		return strings.Contains(out, fmt.Sprintf("%016x", ids["n6"].ID))
	})
	out := run(t, "ip", "netns", "exec", ns["n4"], "ping", "-c", "2", "-W", "2", "10.144.144.3")
	if !strings.Contains(out, "2 received") && !strings.Contains(out, "2 packets received") {
		t.Fatalf("overlay ping failed:\n%s", out)
	}
}

type peerSpec struct {
	name   string
	dialer bool
	addr   string
}

func build(t *testing.T, out, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = moduleRoot(t)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v", pkg, err)
	}
}

// moduleRoot walks up from the test's working directory to the module root so
// relative package paths resolve no matter where the test binary runs.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func peersUp(t *testing.T, namespace, bin, cfg string, want int) bool {
	t.Helper()
	out := run(t, "ip", "netns", "exec", namespace, bin, "-c", cfg, "peers", "--json")
	var peers []struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		return false
	}
	up := 0
	for _, p := range peers {
		if p.State == "up" {
			up++
		}
	}
	return up >= want
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out)
	}
	return string(out)
}

func containsLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}
