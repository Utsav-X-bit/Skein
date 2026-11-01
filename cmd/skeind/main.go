package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/daemon"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/service"
	"github.com/Utsav-X-bit/skein/internal/version"
)

const usage = `skeind — Skein mesh VPN daemon

Usage:
  skeind run -c <config>       start the daemon (root / Administrator)
  skeind install-service -c <config>
  skeind uninstall-service
  skeind version               print the version
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return runDaemon(args[1:])
	case "install-service":
		return installService(args[1:])
	case "uninstall-service":
		if err := service.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
			return 1
		}
		fmt.Println("skein service removed")
		return 0
	case "version":
		fmt.Println("skeind " + version.Version)
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "skeind: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func configPath(args []string) (string, error) {
	path := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c", "--config":
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a path", args[i])
			}
			path = args[i+1]
			i++
		default:
			return "", fmt.Errorf("unexpected argument %q", args[i])
		}
	}
	if path == "" {
		var err error
		if path, err = conf.DefaultPath(); err != nil {
			return "", err
		}
	}
	return path, nil
}

func runDaemon(args []string) int {
	path, err := configPath(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skeind: %v\n\n%s", err, usage)
		return 2
	}
	if err := start(path); err != nil {
		fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
		return 1
	}
	return 0
}

func start(path string) error {
	cfg, err := conf.Load(path)
	if err != nil {
		return err
	}
	if err := conf.CheckPermissions(path); err != nil {
		return err
	}
	identityPath := filepath.Join(filepath.Dir(path), "identity.key")
	id, err := identity.Load(identityPath)
	if err != nil {
		return fmt.Errorf("load identity %s (run `skein init` first): %w", identityPath, err)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("skeind %s starting as %016x", version.Version, id.ID)
	return runFn(ctx)
}

func installService(args []string) int {
	path, err := configPath(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skeind: %v\n\n%s", err, usage)
		return 2
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
		return 1
	}
	absConfig, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
		return 1
	}
	if err := service.Install(executable, absConfig); err != nil {
		fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
		return 1
	}
	fmt.Printf("skein service installed\n  start: systemctl start %s (Linux) or sc start %s (Windows)\n", service.Name, service.Name)
	return 0
}
