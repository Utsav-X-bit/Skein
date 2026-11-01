//go:build !windows

package conf

import "os"

// elevated reports whether the process can configure network interfaces.
func elevated() bool { return os.Geteuid() == 0 }
