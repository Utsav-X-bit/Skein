// Package app holds the daemon entry points shared by the skein and skeind
// binaries, so either one can run the node.
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/daemon"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/service"
	"github.com/Utsav-X-bit/skein/internal/version"
)

// Run loads the configuration and identity and runs the daemon until ctx is
// cancelled. It must be called with root/Administrator privileges.
func Run(ctx context.Context, configPath string) error {
	cfg, err := conf.Load(configPath)
	if err != nil {
		return err
	}
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

// InstallService registers the current executable as a system service that
// runs this node with configPath. It opens the underlay port for IPv4 and IPv6
// immediately, so the node is reachable as soon as the service starts.
func InstallService(configPath string) error {
	cfg, err := conf.Load(configPath)
	if err != nil {
		return err
	}
	if err := firewall.EnsurePort(firewall.Config{Interface: cfg.Node.Interface, Port: cfg.Node.ListenPort}); err != nil {
		log.Printf("skein: warning: firewall: %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	return service.Install(executable, absConfig)
}

// UninstallService removes the service and the firewall rules this node added.
func UninstallService(configPath string) error {
	if cfg, err := conf.Load(configPath); err == nil {
		_ = firewall.Remove(firewall.Config{Interface: cfg.Node.Interface, Port: cfg.Node.ListenPort})
	}
	return service.Uninstall()
}
