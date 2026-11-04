package daemon

import (
	"testing"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

// TestDevTCPEnabled is the Step 5 (M5) gate for the dev-TCP listener:
// dev TCP starts only for a dev config (listen set, no socket/pipe).
// Any config with a kernel transport — in particular every
// system/service config, which always carries a socket or pipe —
// gets the kernel plane only, even if api.listen is also set.
func TestDevTCPEnabled(t *testing.T) {
	cases := []struct {
		name   string
		socket string
		pipe   string
		listen string
		want   bool
	}{
		{"dev listen only", "", "", "127.0.0.1:11011", true},
		{"service socket plus listen", "/run/skein/control.sock", "", "127.0.0.1:11011", false},
		{"service pipe plus listen", "", "skein", "127.0.0.1:11011", false},
		{"service socket only", "/run/skein/control.sock", "", "", false},
		{"no transport", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := conf.Default()
			cfg.API.Socket, cfg.API.Pipe, cfg.API.Listen = tc.socket, tc.pipe, tc.listen
			if got := devTCPEnabled(cfg); got != tc.want {
				t.Fatalf("devTCPEnabled(socket=%q pipe=%q listen=%q) = %v, want %v", tc.socket, tc.pipe, tc.listen, got, tc.want)
			}
		})
	}
}
