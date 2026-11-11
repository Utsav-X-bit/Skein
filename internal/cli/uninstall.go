package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Utsav-X-bit/skein/internal/uninstall"
)

func cmdUninstall(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	yes := false
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Fprintln(out, "Usage: skein uninstall [--yes]\n\nRemove Skein and its local configuration, identity, backups, and user data. Use --yes to skip confirmation.")
			return 0
		case "--yes":
			yes = true
		default:
			fmt.Fprintf(errOut, "skein: unknown uninstall flag %q\n", arg)
			return 2
		}
	}
	if !yes {
		fmt.Fprint(out, "This removes Skein, its local configuration, identity, backups, and Skein data in user profiles. This cannot be undone.\nType yes to continue: ")
		answer, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintf(errOut, "skein: read confirmation: %v\n", err)
			return 1
		}
		if !strings.EqualFold(strings.TrimSpace(answer), "yes") {
			fmt.Fprintln(out, "Uninstall cancelled.")
			return 0
		}
	}
	message, err := uninstall.Run(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "skein: uninstall: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, message)
	return 0
}
