package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/service"
	"github.com/Utsav-X-bit/skein/internal/version"
)

const usage = `skeind — Skein mesh VPN daemon

Usage:
  skeind run -c <config>       start the daemon (root / Administrator)
  skeind install-service -c <config>
  skeind uninstall-service
  skeind version | -v | --version
                               print the version

The same commands exist in the skein binary: skein run, skein install-service,
skein uninstall-service.

The desktop app installs the service with the system config (skein-helper
init/import); run -c with a user config is the dev path.
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
	case "install-service":
		path, err := configPath(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n\n%s", err, usage)
			return 2
		}
		if err := app.InstallService(path); err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
			return 1
		}
		fmt.Printf("skein service installed\n  start: systemctl start %s (Linux) or sc start %s (Windows)\n", service.Name, service.Name)
		return 0
	case "uninstall-service":
		path, err := configPath(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n\n%s", err, usage)
			return 2
		}
		if err := app.UninstallService(path); err != nil {
			fmt.Fprintf(os.Stderr, "skeind: %v\n", err)
			return 1
		}
		fmt.Println("skein service removed")
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
