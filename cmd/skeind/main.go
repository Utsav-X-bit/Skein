package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/daemon"
	"github.com/Utsav-X-bit/skein/internal/version"
)

const usage = `skeind — Skein mesh VPN daemon

Usage:
  skeind run -c <config>       start the daemon (root / Administrator)
  skeind version | -v | --version
                               print the version

Step 12: the install-service/uninstall-service compat aliases are
deleted. Install is helper-only (skein-helper install, via
RegisterDaemon); the skein CLI wraps it as skein install-service.
Service registration goes through the helper, never through skeind.
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
		path, err := configPath(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n\n%s", err, usage)
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := app.Run(ctx, path); err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
			return 1
		}
		return 0
	case "apply-update":
		// Hidden internal subcommand (Step 7): the detached
		// apply process spawned by the daemon's update/apply
		// endpoint. Not in --help, not user-facing, takes no
		// secrets on argv (paths only). See
		// daemon.ApplyUpdateDetached.
		staged, cfgPath, statePath, err := applyUpdateArgs(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
			return 2
		}
		if err := daemon.ApplyUpdateDetached(staged, cfgPath, statePath, nil); err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
			return 1
		}
		return 0
	case "version", "-v", "--version":
		fmt.Println("skeind " + version.Info())
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "skeind: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// applyUpdateArgs parses the hidden apply-update argv:
// --staged <path> --config <path> --state <path>, all required.
func applyUpdateArgs(args []string) (staged, cfgPath, statePath string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--staged", "--config", "--state":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("%s requires a path", args[i])
			}
			switch args[i] {
			case "--staged":
				staged = args[i+1]
			case "--config":
				cfgPath = args[i+1]
			case "--state":
				statePath = args[i+1]
			}
			i++
		default:
			return "", "", "", fmt.Errorf("unexpected argument %q", args[i])
		}
	}
	if staged == "" || cfgPath == "" || statePath == "" {
		return "", "", "", fmt.Errorf("apply-update requires --staged, --config, and --state")
	}
	return staged, cfgPath, statePath, nil
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
		if path, err = conf.DefaultPathForUser(); err != nil {
			return "", err
		}
	}
	return path, nil
}
