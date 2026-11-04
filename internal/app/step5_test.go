package app

import (
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

// TestCheckSystemDevTCP is the Step 5 (M5) app-layer gate: a system
// config that sets api.listen refuses to start (with a remediation),
// instead of silently serving a second bearer-token control plane
// beside the kernel plane. Dev configs are unaffected.
func TestCheckSystemDevTCP(t *testing.T) {
	dev := conf.Default()
	dev.API.Listen = "127.0.0.1:11011"
	noListen := conf.Default()
	noListen.API.Listen = ""

	cases := []struct {
		name string
		path string
		cfg  conf.Config
		want string // "" means no error; otherwise a required substring
	}{
		{"system plus listen refused", SystemConfigPath(), dev, "api.listen"},
		{"system without listen ok", SystemConfigPath(), noListen, ""},
		{"dev plus listen ok", "/home/user/.config/skein/skein.toml", dev, ""},
		{"dev without listen ok", "/home/user/.config/skein/skein.toml", noListen, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSystemDevTCP(tc.path, tc.cfg)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("checkSystemDevTCP = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("checkSystemDevTCP = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}
