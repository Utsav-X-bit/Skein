package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/path"
	"github.com/Utsav-X-bit/skein/internal/service"
	"github.com/Utsav-X-bit/skein/internal/wan"
)

// BootstrapResult is the public metadata returned by init, join, and
// rotate-secret. Only Join is secret-bearing (it carries the network
// secret); the GUI treats it as transient.
type BootstrapResult struct {
	NodeID     string `json:"node_id"`
	PublicKey  string `json:"public_key"`
	Name       string `json:"name"`
	Overlay    string `json:"overlay_ipv4"`
	Invite     string `json:"invite"`
	Join       string `json:"join"`
	ConfigPath string `json:"config_path"`
	Backup     string `json:"backup,omitempty"`
	Service    string `json:"service"`
}

// InitOptions configures helper init. Zero fields keep the built-in
// defaults; MDNS/STUN nil means default (both on).
type InitOptions struct {
	Name       string
	PrefixV4   string
	PrefixV6   string
	Port       uint16
	MTU        int
	MDNS       *bool
	STUN       *bool
	STUNServer string
}

// InitRoot generates identity, secret, and config root-side, installs and
// starts the service, and opens the firewall. It refuses to overwrite an
// existing system config (use ImportRoot to migrate one).
func InitRoot(opts InitOptions) (BootstrapResult, error) {
	cfgPath, keyPath := SystemConfigPath(), SystemIdentityPath()
	if _, err := os.Stat(cfgPath); err == nil {
		return BootstrapResult{}, fmt.Errorf("init: %s already exists; use import to migrate it or reset first", cfgPath)
	}
	if _, err := os.Stat(keyPath); err == nil {
		return BootstrapResult{}, fmt.Errorf("init: %s already exists; use reset first", keyPath)
	}

	cfg := conf.Default()
	if opts.Name != "" {
		if err := conf.ValidateName(opts.Name); err != nil {
			return BootstrapResult{}, err
		}
		cfg.Node.Name = opts.Name
	} else {
		cfg.Node.Name = conf.DefaultNodeName()
	}
	if opts.Port != 0 {
		cfg.Node.ListenPort = opts.Port
	}
	if opts.MTU != 0 {
		cfg.Node.MTU = opts.MTU
	}
	if opts.MDNS != nil {
		cfg.Discovery.MDNS = *opts.MDNS
	}
	if opts.STUN != nil {
		cfg.Discovery.STUN = *opts.STUN
	}
	if opts.STUNServer != "" {
		cfg.Discovery.STUNServer = opts.STUNServer
	}
	if opts.PrefixV4 != "" {
		p, err := netip.ParsePrefix(opts.PrefixV4)
		if err != nil {
			return BootstrapResult{}, fmt.Errorf("init: overlay prefix v4: %w", err)
		}
		cfg.Node.OverlayIPv4Prefix = p
	}
	if opts.PrefixV6 != "" {
		p, err := netip.ParsePrefix(opts.PrefixV6)
		if err != nil {
			return BootstrapResult{}, fmt.Errorf("init: overlay prefix v6: %w", err)
		}
		cfg.Node.OverlayIPv6Prefix = p
	}
	addr, err := conf.NextFreeOverlay(cfg.Node.OverlayIPv4Prefix, nil)
	if err != nil {
		return BootstrapResult{}, err
	}
	cfg.Node.OverlayIPv4 = addr
	secret, err := randomSecret()
	if err != nil {
		return BootstrapResult{}, err
	}
	cfg.Node.NetworkSecret = secret
	setControlTransport(&cfg)

	id, err := identity.Generate()
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := writeRoot(cfg, id); err != nil {
		return BootstrapResult{}, err
	}
	service, err := ensureService(cfg)
	if err != nil {
		return BootstrapResult{}, err
	}
	res, err := bootstrapResult(cfg, id, service)
	if err != nil {
		return BootstrapResult{}, err
	}
	return res, nil
}

// JoinRoot configures this node root-side from a network invite: secret,
// overlay address, and the inviter as a dialer peer. It generates a fresh
// identity when none exists, migrates any legacy api.listen/token to the
// socket/pipe transport, and leaves the service installed and running.
func JoinRoot(inviteText string) (BootstrapResult, error) {
	inv, err := identity.DecodeNetworkInvite(inviteText)
	if err != nil {
		return BootstrapResult{}, err
	}
	cfgPath, keyPath := SystemConfigPath(), SystemIdentityPath()

	cfg := conf.Default()
	if raw, err := os.ReadFile(cfgPath); err == nil {
		if cfg, err = conf.Parse(string(raw)); err != nil {
			return BootstrapResult{}, fmt.Errorf("join: existing %s: %w", cfgPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return BootstrapResult{}, err
	}

	var id identity.Identity
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		if id, err = identity.Generate(); err != nil {
			return BootstrapResult{}, err
		}
	} else if err != nil {
		return BootstrapResult{}, err
	} else if id, err = identity.Load(keyPath); err != nil {
		return BootstrapResult{}, err
	}

	cfg.Node.NetworkSecret = inv.Secret
	cfg.Node.OverlayIPv4Prefix = inv.PrefixV4
	cfg.Node.OverlayIPv6Prefix = inv.PrefixV6
	if cfg.Node.Name == "" {
		cfg.Node.Name = conf.DefaultNodeName()
	}

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
			return BootstrapResult{}, err
		}
		cfg.Node.OverlayIPv4 = addr
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
	setControlTransport(&cfg)
	if err := conf.Validate(cfg); err != nil {
		return BootstrapResult{}, err
	}
	if err := writeRoot(cfg, id); err != nil {
		return BootstrapResult{}, err
	}
	service, err := ensureService(cfg)
	if err != nil {
		return BootstrapResult{}, err
	}
	return bootstrapResult(cfg, id, service)
}

// RotateRoot backs up the config, stops the daemon, replaces the network
// secret, and starts it again. Every peer must rejoin with the new join
// string the result carries.
func RotateRoot() (BootstrapResult, error) {
	cfgPath, keyPath := SystemConfigPath(), SystemIdentityPath()
	cfg, err := conf.Load(cfgPath)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("rotate: %w", err)
	}
	id, err := identity.Load(keyPath)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("rotate: %w", err)
	}
	backup, err := backupFiles(cfgPath, keyPath)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := service.Stop(); err != nil {
		log.Printf("skein-helper: rotate: %v", err)
	}
	secret, err := randomSecret()
	if err != nil {
		return BootstrapResult{}, err
	}
	cfg.Node.NetworkSecret = secret
	if err := writeRoot(cfg, id); err != nil {
		return BootstrapResult{}, err
	}
	if err := service.Start(); err != nil {
		return BootstrapResult{}, fmt.Errorf("rotate: daemon stopped and secret rotated (backup %s): %w", backup, err)
	}
	res, err := bootstrapResult(cfg, id, service.StateRunning)
	if err != nil {
		return BootstrapResult{}, err
	}
	res.Backup = backup
	return res, nil
}

// ImportRoot migrates a user-mode config into the system path: it validates
// the source (regular file, owner-only, parent chain not world-writable),
// backs up any existing system config, writes the source with the
// socket/pipe transport enabled and legacy api.listen/token disabled,
// rewrites the service binPath, and starts the service.
func ImportRoot(source string) (BootstrapResult, error) {
	cfgPath, keyPath := SystemConfigPath(), SystemIdentityPath()
	srcKey := filepath.Join(filepath.Dir(source), "identity.key")

	if _, err := validateSourceFile(source); err != nil {
		return BootstrapResult{}, err
	}
	cfg, err := conf.Load(source)
	if err != nil {
		return BootstrapResult{}, err
	}
	if _, err := os.Stat(srcKey); err != nil {
		return BootstrapResult{}, fmt.Errorf("import: %s (identity) is missing; the source directory must hold identity.key", srcKey)
	}
	id, err := identity.Load(srcKey)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("import: %w", err)
	}

	backup, err := backupFiles(cfgPath, keyPath)
	if err != nil {
		return BootstrapResult{}, err
	}
	// Copy the identity in place of the old one (backed up above), then the
	// config, so a failed config write never strands a new key.
	if err := copyRootFile(srcKey, keyPath); err != nil {
		return BootstrapResult{}, err
	}
	if cfg.Node.OverlayIPv4.IsValid() {
		used := append([]netip.Addr{}, cfg.Node.OverlayIPv4)
		if !cfg.Node.OverlayIPv4Prefix.Contains(used[0]) {
			return BootstrapResult{}, fmt.Errorf("import: overlay %s is not in prefix %s", used[0], cfg.Node.OverlayIPv4Prefix)
		}
	} else {
		addr, err := conf.NextFreeOverlay(cfg.Node.OverlayIPv4Prefix, nil)
		if err != nil {
			return BootstrapResult{}, err
		}
		cfg.Node.OverlayIPv4 = addr
	}
	setControlTransport(&cfg)
	if err := conf.Validate(cfg); err != nil {
		return BootstrapResult{}, err
	}
	if err := writeConfigRoot(cfg); err != nil {
		return BootstrapResult{}, err
	}
	state, err := ensureService(cfg)
	if err != nil {
		return BootstrapResult{}, err
	}
	res, err := bootstrapResult(cfg, id, state)
	if err != nil {
		return BootstrapResult{}, err
	}
	res.Backup = backup
	return res, nil
}

// ResetRoot stops the service and removes the system config and identity.
// Without purge the files move to a backup directory; keepIdentity retains
// the key so a re-init keeps the node ID.
func ResetRoot(purge, keepIdentity bool) (BackupResult, error) {
	cfgPath, keyPath := SystemConfigPath(), SystemIdentityPath()
	if err := service.Stop(); err != nil {
		log.Printf("skein-helper: reset: %v", err)
	}
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		if _, err2 := os.Stat(keyPath); errors.Is(err2, os.ErrNotExist) {
			return BackupResult{Message: "nothing to remove"}, nil
		}
	}
	if purge {
		if err := os.Remove(cfgPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return BackupResult{}, err
		}
		if !keepIdentity {
			if err := os.Remove(keyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return BackupResult{}, err
			}
		}
		return BackupResult{Message: "config and identity removed"}, nil
	}
	backup, err := backupFiles(cfgPath, keyPath)
	if err != nil {
		return BackupResult{}, err
	}
	if !keepIdentity {
		if err := os.Remove(keyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return BackupResult{}, err
		}
	}
	return BackupResult{Backup: backup}, nil
}

// BackupResult reports what reset did with the files.
type BackupResult struct {
	Backup  string `json:"backup,omitempty"`
	Message string `json:"message,omitempty"`
}

// setControlTransport points the config at the platform control plane and
// disables the legacy dev TCP transport, so the GUI connects over the
// socket/pipe and no bearer token is needed.
func setControlTransport(cfg *conf.Config) {
	cfg.API.Socket = ""
	cfg.API.Pipe = ""
	cfg.API.Listen = ""
	cfg.API.Token = nil
	setControlField(cfg)
}

// EnsureService loads the system config and leaves the service installed
// and running for it, opening the firewall. The helper install verb and
// bootstrap verbs share it.
func EnsureService() (service.State, error) {
	cfg, err := conf.Load(SystemConfigPath())
	if err != nil {
		return service.StateUnknown, fmt.Errorf("no system config at %s: run init or import first", SystemConfigPath())
	}
	return ensureService(cfg)
}

// ensureService leaves the service installed and running for cfg, opening
// the firewall on the way. A firewall failure only warns: the daemon
// re-applies the interface rule when it creates the adapter.
func ensureService(cfg conf.Config) (service.State, error) {
	// Migrate older configs to the socket/pipe transport before the service
	// starts; otherwise the desktop has no control endpoint to connect to.
	setControlTransport(&cfg)
	if err := writeConfigRoot(cfg); err != nil {
		return service.StateError, fmt.Errorf("service config: %w", err)
	}
	if err := grantBootstrapUser(); err != nil {
		return service.StateError, fmt.Errorf("grant user: %w", err)
	}
	// Only the port can be opened before the service creates its TUN
	// interface. The daemon adds the interface-scoped rules after startup.
	if err := firewall.EnsurePort(firewall.Config{Interface: cfg.Node.Interface, Port: cfg.Node.ListenPort}); err != nil {
		log.Printf("skein-helper: firewall: %v", err)
	}
	exe, _, installed, err := service.Installed()
	if err != nil {
		return service.StateUnknown, err
	}
	if installed {
		if err := service.Rewrite(exe, SystemConfigPath()); err != nil {
			return service.StateUnknown, err
		}
	} else {
		daemon, err := daemonExecutable()
		if err != nil {
			return service.StateUnknown, err
		}
		if err := service.Install(daemon, SystemConfigPath()); err != nil {
			return service.StateUnknown, err
		}
	}
	if err := service.Start(); err != nil {
		return service.StateError, fmt.Errorf("service start: %w", err)
	}
	// `sc start` can return success just before the service exits. Wait for a
	// settled state so onboarding reports a failed start instead of leaving
	// the desktop on "daemon offline".
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err := service.ReadStatus()
		if err == nil {
			switch st.State {
			case service.StateRunning:
				return st.State, nil
			case service.StateStopped, service.StateError:
				return st.State, fmt.Errorf("service did not stay running (state: %s)", st.State)
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return service.StateUnknown, fmt.Errorf("service state: %w", err)
			}
			return st.State, fmt.Errorf("service did not reach running (state: %s)", st.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// daemonExecutable finds the installed daemon binary. skeind is the thin
// wrapper; a full skein binary also has `run`, so either works. The helper
// must never register itself as the service: it only performs bootstrap verbs.
func daemonExecutable() (string, error) {
	names := []string{"skeind", "skein"}
	if runtime.GOOS == "windows" {
		names = []string{"skeind.exe", "skein.exe"}
	}
	// A dist bundle keeps the helper and daemon together; prefer that before
	// PATH and the install directory.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range names {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
		if runtime.GOOS == "windows" {
			p := filepath.Join(os.Getenv("ProgramFiles"), "Skein", name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
			continue
		}
		for _, p := range []string{"/usr/local/bin/" + name, "/usr/bin/" + name, "/usr/libexec/" + name} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("neither skeind nor skein is installed; install the Skein package first")
}

// bootstrapResult renders the public metadata, the peer invite, and the
// secret-bearing join string for cfg.
func bootstrapResult(cfg conf.Config, id identity.Identity, state service.State) (BootstrapResult, error) {
	cands, err := publicCandidates(cfg)
	if err != nil {
		return BootstrapResult{}, err
	}
	invite, err := identity.EncodeInvite(identity.Invite{ID: id.ID, PublicKey: id.PublicKey, Addrs: cands})
	if err != nil {
		return BootstrapResult{}, err
	}
	used := dedupeOverlays([]netip.Addr{cfg.Node.OverlayIPv4}, cfg.Node.OverlayIPv4Prefix)
	join, err := identity.EncodeNetworkInvite(identity.NetworkInvite{
		Secret:    cfg.Node.NetworkSecret,
		ID:        id.ID,
		PublicKey: id.PublicKey,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
		UsedV4:    used,
		Addrs:     cands,
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{
		NodeID:     fmt.Sprintf("%016x", id.ID),
		PublicKey:  base64.StdEncoding.EncodeToString(id.PublicKey),
		Name:       cfg.Node.Name,
		Overlay:    cfg.Node.OverlayIPv4.String(),
		Invite:     invite,
		Join:       join,
		ConfigPath: SystemConfigPath(),
		Service:    string(state),
	}, nil
}

// publicCandidates lists this node's dialable endpoints: local interface
// addresses plus the configured public address and STUN discovery.
func publicCandidates(cfg conf.Config) ([]netip.AddrPort, error) {
	cands, err := path.LocalCandidates(cfg.Node.ListenPort)
	if err != nil {
		return nil, err
	}
	cands = path.ExcludePrefixes(cands, []netip.Prefix{
		cfg.Node.OverlayIPv4Prefix,
		cfg.Node.OverlayIPv6Prefix,
	})
	return wan.Merge(cands, wan.Candidates(context.Background(), cfg)), nil
}

// writeConfigRoot renders cfg and writes it to the system path atomically,
// root-owned.
func writeConfigRoot(cfg conf.Config) error {
	text, err := conf.Render(cfg)
	if err != nil {
		return err
	}
	return writeRootBytes(SystemConfigPath(), text)
}

// writeRoot writes config and identity root-side.
func writeRoot(cfg conf.Config, id identity.Identity) error {
	if err := os.MkdirAll(SystemDir(), 0o750); err != nil {
		return err
	}
	if err := fileperm.Root(SystemDir()); err != nil {
		return err
	}
	if err := identity.Save(SystemIdentityPath(), id); err != nil {
		return err
	}
	if err := fileperm.Root(SystemIdentityPath()); err != nil {
		return err
	}
	return writeConfigRoot(cfg)
}

// writeRootBytes writes text to path atomically with root ownership.
func writeRootBytes(path string, text []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := fileperm.Root(name); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// copyRootFile copies src to dst atomically with root ownership.
func copyRootFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeRootBytes(dst, data)
}

// validateSourceFile checks a file being imported root-side: it must be a
// regular file (not a symlink), owner-only readable, and its parent chain
// must not contain a world-writable directory.
func validateSourceFile(src string) (os.FileInfo, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("import: %s is not a regular file", src)
	}
	if isWorldWritable(info.Mode()) {
		return nil, fmt.Errorf("import: %s is world-accessible (mode %o); chmod 600 first", src, info.Mode().Perm())
	}
	dir := filepath.Dir(src)
	for {
		di, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		if di.IsDir() && isWorldWritable(di.Mode()) {
			return nil, fmt.Errorf("import: parent directory %s is world-writable", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return info, nil
}

// isWorldWritable reports whether group or others have any access.
func isWorldWritable(mode os.FileMode) bool { return mode&0o077 != 0 }

// backupFiles moves the existing system config and identity into a
// timestamped directory under BackupRoot. Missing files are skipped; it
// returns the backup path ("" when nothing existed).
func backupFiles(paths ...string) (string, error) {
	existing := 0
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			existing++
		}
	}
	if existing == 0 {
		return "", nil
	}
	stamp := time.Now().Format("20060102-150405")
	dst := filepath.Join(BackupRoot(), stamp)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return "", err
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Rename(p, filepath.Join(dst, filepath.Base(p))); err != nil {
			return "", err
		}
	}
	return dst, nil
}

func randomSecret() ([]byte, error) {
	secret := make([]byte, identity.KeySize)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
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

// execOutput runs a command and returns its combined output.
func execOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w: %s", name, args, err, out)
	}
	return string(out), nil
}
