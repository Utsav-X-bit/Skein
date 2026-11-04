// Package app holds the daemon entry points shared by the skein and skeind
// binaries, so either one can run the node.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Utsav-X-bit/skein/internal/bootstrap"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/daemon"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/version"
)

// Run loads the configuration and identity and runs the daemon until ctx is
// cancelled. It must be called with root/Administrator privileges.
//
// Step 4 (ADR-001 §2.2): if the system config is missing, Run does not
// fail the service into a restart loop. Instead it checks the bootstrap
// state: a valid, unused, unexpired token means the service was
// started by the minimal helper's bootstrap verb, so Run runs
// bootstrap mode and transitions in-process to normal mode; no valid
// state means the node is simply not configured yet, and Run logs
// that and exits 0 (the GUI/helper starts it again after minting a
// token). A dev (non-system) config that is missing is still an
// error — bootstrap mode is a system-service state, not a dev path.
func Run(ctx context.Context, configPath string) error {
	cfg, err := conf.Load(configPath)
	if err != nil {
		if configPath == SystemConfigPath() {
			if st, serr := bootstrap.Load(BootstrapStatePath()); serr == nil && st.Valid() {
				// M9 §2c: this branch previously returned
				// RunBootstrap directly, bypassing the service
				// wrapper below. A service-launched bootstrap
				// daemon then never called
				// StartServiceCtrlDispatcher, so the SCM timed out
				// the start (1053) even though the daemon was
				// serving the bootstrap pipe. Both daemon modes
				// must go through the same service decision.
				return runWithService(ctx, func(ctx context.Context) error {
					return RunBootstrap(ctx, configPath)
				})
			}
		}
		if configPath == SystemConfigPath() {
			// Unconfigured, no valid bootstrap: a clean exit, not a
			// crash the service manager would restart-loop.
			if _, serr := os.Stat(configPath); os.IsNotExist(serr) {
				log.Printf("skein: not configured; run bootstrap via the GUI or minimal helper")
				return nil
			}
		}
		return err
	}
	// Step 5 (ADR-001 §5): a system config that also sets api.listen
	// refuses to start, rather than silently serving a second,
	// bearer-token control plane beside the kernel plane. Normal
	// install/bootstrap flows already clear api.listen before the
	// service starts, so this fires only for a hand-edited or
	// otherwise broken system config. Checked before the leftover
	// bootstrap-state deletion, so a refusal does not destroy state.
	if err := checkSystemDevTCP(configPath, cfg); err != nil {
		return err
	}
	// A config exists: any leftover bootstrap state is dead and is
	// deleted, so a later restart can never re-enter bootstrap mode.
	_ = bootstrap.DeleteState(BootstrapStatePath())
	if err := conf.CheckPermissions(configPath); err != nil {
		return err
	}
	if err := checkSystemOwnership(configPath); err != nil {
		return err
	}
	keyPath := filepath.Join(filepath.Dir(configPath), "identity.key")
	id, err := identity.Load(keyPath)
	if err != nil {
		return fmt.Errorf("load identity %s (run `skein init` first): %w", keyPath, err)
	}

	runFn := func(ctx context.Context) error {
		d, err := daemon.New(cfg, id)
		if err != nil {
			return err
		}
		defer d.Close()
		return d.Run(ctx)
	}

	if asService, err := isWindowsService(); err == nil && asService {
		return runAsService(runFn)
	}

	log.Printf("skein daemon %s starting as %016x", version.Version, id.ID)
	return runFn(ctx)
}

// runWithService runs fn directly, or via the Windows service
// wrapper when this process was launched by the SCM. A
// service-launched process that never reports to the SCM times out
// its start (SCM 1053), so every daemon mode — normal and bootstrap
// — must route through here. A failed launch-mode check falls back
// to a direct run, matching the normal path's historical guard.
func runWithService(ctx context.Context, fn func(context.Context) error) error {
	if asService, err := isWindowsService(); err == nil && asService {
		return runAsService(fn)
	}
	return fn(ctx)
}

// checkSystemDevTCP implements the Step 5 service-path rule: the
// installed service serves the control API only on the kernel
// transport (socket/pipe). A system config with api.listen set is a
// refusal with a remediation, never a second listener and never a
// silent ignore. Dev (non-system) configs are unaffected — dev TCP
// is legitimate there, bearer-capped at read by the API gate.
func checkSystemDevTCP(configPath string, cfg conf.Config) error {
	if configPath == SystemConfigPath() && cfg.API.Listen != "" {
		return fmt.Errorf("system config %s sets api.listen %q: the installed service serves the control API only on the socket/pipe transport; clear api.listen (helper install/bootstrap already clears it) and restart", configPath, cfg.API.Listen)
	}
	return nil
}

// Step 12: InstallService/UninstallService are deleted, not kept behind
// a flag. Install is helper-only: the minimal helper's install verb
// goes through EnsureServiceDaemon -> service.RegisterDaemon (Step 6),
// and uninstall goes through the helper's uninstall path. Any caller
// that reached for these wrappers was the skeind compat alias, also
// deleted in Step 12.
