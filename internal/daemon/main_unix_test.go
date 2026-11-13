//go:build unix && !android

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "skein-daemon-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Never load the real installation's grants/state or chown a test socket
	// to root:skein. Transport authentication and role gates remain unchanged.
	controlAccessFile = filepath.Join(dir, "access")
	controlSocketGroup = ""
	stateFileOverride = filepath.Join(dir, "state.json")
	result := m.Run()
	os.RemoveAll(dir)
	os.Exit(result)
}
