package app

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/service"
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
// and running for it, opening the firewall. It is the helper install
// verb's path (Step 12: the legacy root entry points are deleted;
// bootstrap installs via RegisterDaemon directly and does not use
// EnsureService).
func EnsureService() (service.State, error) {
	cfg, err := conf.Load(SystemConfigPath())
	if err != nil {
		return service.StateUnknown, fmt.Errorf("no system config at %s: run init or import first", SystemConfigPath())
	}
	return ensureService(cfg)
}

// EnsureServiceDaemon is EnsureService with an explicit daemon
// candidate (helper `install --daemon <path>`). The path is
// validated by RegisterDaemon exactly like a package candidate;
// it is the plan §6.1(a) installer-supplied path, not a search.
func EnsureServiceDaemon(daemonPath string) (service.State, error) {
	cfg, err := conf.Load(SystemConfigPath())
	if err != nil {
		return service.StateUnknown, fmt.Errorf("no system config at %s: run init or import first", SystemConfigPath())
	}
	return ensureServiceWithDaemon(cfg, daemonPath)
}

// ensureService leaves the service installed and running for cfg, opening
// the firewall on the way. A firewall failure only warns: the daemon
// re-applies the interface rule when it creates the adapter.
func ensureService(cfg conf.Config) (service.State, error) {
	return ensureServiceWithDaemon(cfg, "")
}

// ensureServiceWithDaemon is ensureService with an explicit daemon
// candidate for RegisterDaemon (helper `install --daemon <path>`);
// empty means package-directory search only. See RegisterDaemon
// for why the candidate is never derived from the helper's
// directory, PATH, or the already-installed executable.
func ensureServiceWithDaemon(cfg conf.Config, daemonPath string) (service.State, error) {
	// Step 3: a running daemon holds the config lock, so the service
	// must be stopped BEFORE the config is rewritten, not after. Stopping
	// an already-stopped service succeeds on Linux (systemctl stop) and
	// is tolerated on Windows (sc 1062), so this is safe when the
	// service is installed but not running.
	if _, _, installed, err := service.Installed(); err != nil {
		return service.StateUnknown, err
	} else if installed {
		if err := service.Stop(); err != nil {
			return service.StateError, fmt.Errorf("service stop: %w", err)
		}
	}
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
	// Step 6: single validated registration. This replaces the old
	// "rewrite the already-installed exe" upgrade bug (Agent D P1)
	// and the daemonExecutable() helper-dir/PATH search (Agent E
	// F-06) with RegisterDaemon: package skeind only, unless the
	// caller supplied an explicit, validated path.
	if _, err := service.RegisterDaemon(service.RegisterOptions{
		ConfigPath: SystemConfigPath(),
		DaemonPath: daemonPath,
	}); err != nil {
		return service.StateUnknown, err
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

// NOTE (Step 6): daemonExecutable() is deleted, not kept behind a
// flag. Its "helper's directory first, then PATH" search was the
// Agent E F-06 planted-binary hole, and its "skein also works"
// fallback violated the one-service-identity rule (skeind only).
// Service registration goes through service.RegisterDaemon only:
// package-directory search, or an explicit validated path. There
// is deliberately no dev-only fallback for service installs — a
// dev-built binary is run with `skeind run`, not registered.

// writeConfigRoot writes cfg to the system path through configstore, the
// single config writer (Step 3), then restores the root-owned invariant
// (configstore itself only restricts to 0600; the creator here is root,
// and Root makes the ownership explicit rather than incidental).
func writeConfigRoot(cfg conf.Config) error {
	store, err := configstore.Open(SystemConfigPath())
	if err != nil {
		return err
	}
	defer store.Close()
	return writeConfigViaStore(store, cfg)
}

// writeConfigViaStore writes cfg through an already-open store and then
// applies root ownership.
func writeConfigViaStore(store *configstore.Store, cfg conf.Config) error {
	if err := store.Write(cfg); err != nil {
		return err
	}
	return fileperm.Root(SystemConfigPath())
}

// writeAtomicBytes writes data to path atomically with root ownership.
// It is deliberately NOT a config writer: its only remaining caller is
// the access-file grant verb. Config files must go through configstore.
func writeAtomicBytes(path string, data []byte) error {
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
	if _, err := tmp.Write(data); err != nil {
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

// execOutput runs a command and returns its combined output.
func execOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w: %s", name, args, err, out)
	}
	return string(out), nil
}
