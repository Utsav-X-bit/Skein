package cli

import (
	"os"
	"testing"
)

// TestMain keeps unit tests offline: STUN discovery would otherwise dial a
// public server on every init/invite.
func TestMain(m *testing.M) {
	_ = os.Setenv("SKEIN_NO_STUN", "1")
	os.Exit(m.Run())
}
