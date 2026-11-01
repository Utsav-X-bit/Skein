//go:build !windows

package fileperm

import "os"

func restrict(path string) error {
	return os.Chmod(path, 0o600)
}
