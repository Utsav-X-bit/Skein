//go:build !windows

package cli

import "os"

func isRoot() bool { return os.Geteuid() == 0 }
