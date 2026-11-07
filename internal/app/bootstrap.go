// Bootstrap support for ADR-001 Step 4: the minimal helper mints a
// single-use token (MintBootstrap), and app.Run detects an
// unconfigured daemon with a valid bootstrap state and runs it in
// bootstrap mode before transitioning in-process to normal mode
// (RunBootstrap). The daemon is the only config writer throughout;
// the helper writes the bootstrap-state file, a least-privilege
// (read) transport admission for the invoking user, and service
// registration; the daemon writes config, identity, and (on success)
// the access-file grant.
package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Utsav-X-bit/skein/internal/bootstrap"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/daemon"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/service"
)

// BootstrapStatePath is the bootstrap-state file in the system
// directory (e.g. /etc/skein/bootstrap.json).
func BootstrapStatePath() string {
	return bootstrap.StatePath(SystemDir())
}

// MintBootstrap mints a single-use bootstrap token for verb
// (init, join, or import) and starts the service in bootstrap mode.
// It runs elevated, under a brief config lock: it fails fast with
// ErrLocked if a daemon already holds the lock, fails if a system
// config already exists, and fails if a valid bootstrap is already in
// progress. An identity.key preserved by `system reset --keep-identity`
// is allowed (and validated): the daemon reuses it rather than
// generating a new identity. Before the service starts, the invoking
// user is admitted to the control socket/pipe with the least role
// (read), because the listener's admission list is derived from the
// access file and a fresh install has no skein group or prior grant;
// the single-use token is still the only bootstrap authorisation.
// The plaintext token is returned as a base64url string for the
// helper's stdout JSON only; the byte slice is zeroed here after
// encoding, and the token is never logged and never written to disk.
// A service-start failure does NOT delete the state: the token stays
// valid for its 10 minutes, so the GUI can retry the start without a
// fresh elevation.
// mintBootstrapToken is the testable core of MintBootstrap: lock,
// already-configured refusal, caller binding, and minting — without
// the root-ownership step or the service install/start, which need a
// real system install (M8) and are not unit-testable against temp
// paths. MintBootstrap wraps it with those production steps.
func mintBootstrapToken(verb, cfgPath, keyPath, statePath string) (string, error) {
	if !bootstrap.ValidVerb(verb) {
		return "", fmt.Errorf("bootstrap: unknown verb %q (want init, join, or import)", verb)
	}
	// Fresh installs have no system config directory yet, and
	// configstore.Open requires its parent to exist (it creates the
	// lock file, not the directory). Without this, helper bootstrap
	// fails on every truly fresh system with ENOENT on the lock path
	// — caught by the M9 §2b packaged-install test, where /etc/skein
	// did not exist. The helper runs elevated, so the directory is
	// root-owned; 0750 matches the daemon's later ownership and
	// permission checks. Creating an existing directory is a no-op,
	// and the already-exists checks below still gate on the config
	// and identity files, not the directory.
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o750); err != nil {
		return "", fmt.Errorf("bootstrap: create config dir: %w", err)
	}
	// An older directory ACL may have stripped the existing lock's permissions.
	// Restore inherited Windows access before configstore opens it.
	if err := fileperm.RestrictDACL(filepath.Dir(cfgPath)); err != nil {
		return "", fmt.Errorf("bootstrap: config directory DACL: %w", err)
	}
	store, err := configstore.Open(cfgPath)
	if err != nil {
		return "", fmt.Errorf("bootstrap: %w", err)
	}
	defer store.Close()
	if err := store.Lock(); err != nil {
		return "", fmt.Errorf("bootstrap: %w", err)
	}
	defer store.Unlock()
	if _, err := os.Stat(cfgPath); err == nil {
		return "", fmt.Errorf("bootstrap: %s already exists; use reset first", cfgPath)
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("bootstrap: %w", err)
	}
	// A key may legitimately exist after `system reset --keep-identity`
	// (or after an interrupted bootstrap that wrote only the key). The
	// daemon reuses it for init/join/import instead of generating a new
	// one, so minting must allow it. Validate it here so a malformed or
	// too-permissive key fails before any elevation work; a missing key
	// is the ordinary fresh-install case.
	if _, err := identity.Load(keyPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("bootstrap: existing identity: %w", err)
	}
	caller, err := bootstrap.InvokingCallerID()
	if err != nil {
		return "", err
	}
	token, err := bootstrap.Mint(statePath, caller, verb)
	if err != nil {
		return "", err
	}
	defer clear(token)
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func MintBootstrap(verb string) (string, error) {
	cfgPath := SystemConfigPath()
	statePath := BootstrapStatePath()
	token, err := mintBootstrapToken(verb, cfgPath, SystemIdentityPath(), statePath)
	if err != nil {
		return "", err
	}
	if err := fileperm.Root(statePath); err != nil {
		_ = os.Remove(statePath)
		return "", fmt.Errorf("bootstrap: own state file: %w", err)
	}
	// The daemon derives the control socket/pipe admission list from the
	// access file when it binds. A fresh Linux install has no skein group
	// and no grant for the invoking user, so without this the token's
	// holder cannot connect to post it. This writes the least role
	// (read), never secret or mutate; the token remains the bootstrap
	// authorisation and commit upgrades the bound caller on success.
	if err := grantBootstrapTransportAccess(); err != nil {
		_ = os.Remove(statePath)
		return "", fmt.Errorf("bootstrap: grant transport access: %w", err)
	}
	// Install the service pointing at the daemon binary with the
	// system config path, then start it. This deliberately does NOT
	// use EnsureService: there is no config to load yet, no firewall
	// port to open (bootstrap mode binds no UDP port), and the only
	// access-file write here is the read transport admission above —
	// the daemon grants the bootstrap caller secret on success.
	// Step 6: the install goes through RegisterDaemon — package
	// skeind only, validated (skeind basename, regular non-symlink,
	// non-world-writable parent). The Step 4 deployment blocker
	// (MintBootstrap via the daemonExecutable() helper-dir/PATH
	// search, Agent E F-06) is closed here: that search is deleted
	// and no bootstrap install path can reach it.
	if _, err := service.RegisterDaemon(service.RegisterOptions{ConfigPath: cfgPath}); err != nil {
		return "", fmt.Errorf("bootstrap: service register: %w", err)
	}
	if err := service.Start(); err != nil {
		return "", fmt.Errorf("bootstrap: service start: %w", err)
	}
	deadline := time.Now().Add(15 * time.Second)
waitRunning:
	for {
		st, err := service.ReadStatus()
		if err == nil {
			switch st.State {
			case service.StateRunning:
				break waitRunning
			case service.StateStopped, service.StateError:
				return "", fmt.Errorf("bootstrap: service did not stay running (state: %s)", st.State)
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return "", fmt.Errorf("bootstrap: service state: %w", err)
			}
			return "", fmt.Errorf("bootstrap: service did not reach running (state: %s)", st.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The service reports Running as soon as systemd execs the daemon,
	// but the bootstrap endpoint only exists once the daemon binds the
	// control socket/pipe — and the CLI posts immediately after this
	// returns. A token returned before the plane exists is unusable
	// (M9 §2b failure: dial unix /run/skein/control.sock: no such
	// file). Wait for the plane itself, on a second bounded deadline,
	// so every client (CLI, GUI) gets a token it can actually post.
	if err := waitBootstrapPlane(time.Now().Add(15 * time.Second)); err != nil {
		return "", fmt.Errorf("bootstrap: control plane did not appear: %w", err)
	}
	return token, nil
}

// RunBootstrap runs an unconfigured daemon in bootstrap mode and
// transitions in-process to a normal daemon on success. It is called
// by Run only when the system config is missing but a valid bootstrap
// state exists. On success it returns the normal daemon's Run error
// (nil when ctx is cancelled); on bootstrap failure it returns the
// failure, and the process exits so the service manager can restart
// it back into bootstrap mode while the token is still valid.
func RunBootstrap(ctx context.Context, configPath string) error {
	socket, pipe := bootstrapTransport()
	bs, err := daemon.NewBootstrap(daemon.BootstrapOptions{
		ConfigPath:   configPath,
		IdentityPath: SystemIdentityPath(),
		StatePath:    BootstrapStatePath(),
		AccessPath:   bootstrapAccessPath(),
		Socket:       socket,
		Pipe:         pipe,
	})
	if err != nil {
		return err
	}
	out, err := bs.Serve(ctx)
	if err != nil {
		bs.Close()
		// A stop/shutdown during bootstrap serve is a clean stop,
		// not a service failure: the normal path (d.Run via
		// connLoop) likewise returns nil on ctx cancellation, and
		// the service handler maps any non-nil run return to a
		// failure exit. Only a genuine context error is
		// translated; unrelated errors still propagate.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("bootstrap: %w", err)
	}
	// Take the control listener before releasing the config lock: the
	// normal daemon re-acquires that lock in daemon.New, and the
	// listener must stay bound across the whole transition so a client
	// that connected right after success is served by the normal API
	// (B16), not by a closed socket/pipe.
	handoff := bs.TakeHandoff()
	bs.Close()
	fail := func(err error) error {
		if handoff != nil {
			handoff.Fail(err)
		}
		return err
	}
	// The bootstrap wrote config + identity; the normal path's
	// permission and ownership checks now apply, before any mesh work.
	// Step 5: the transition into normal mode is also a service
	// path, so a committed config that somehow still sets api.listen
	// refuses here instead of serving dev TCP as a service.
	if err := checkSystemDevTCP(configPath, out.Config); err != nil {
		return fail(err)
	}
	if err := conf.CheckPermissions(configPath); err != nil {
		return fail(err)
	}
	if err := checkSystemOwnership(configPath); err != nil {
		return fail(err)
	}
	d, err := daemon.New(out.Config, out.ID)
	if err != nil {
		return fail(err)
	}
	d.AdoptControlHandoff(handoff)
	handoff = nil
	defer d.Close()
	// Live-server grant, in addition to the persisted access file the
	// daemon just loaded at construction: the bootstrap caller holds
	// the secret role from this moment, with no restart in between.
	d.GrantSecret(out.CallerID)
	log.Printf("skein: bootstrap complete for %s; starting normal mode", out.CallerID)
	return d.Run(ctx)
}
