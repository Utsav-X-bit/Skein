//go:build !android

package services

import "github.com/Utsav-X-bit/skein/internal/app"

// liveSystemDir is the privileged install root the GUI must never
// write (app.SystemDir: /etc/skein on Unix, the redirected
// ProgramData dir on Windows). Read live, not captured at init.
func liveSystemDir() string { return app.SystemDir() }
