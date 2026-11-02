//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT turns on ANSI escape processing for the Windows console.
func enableVT() func() {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return func() {}
	}
	_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	return func() { _ = windows.SetConsoleMode(handle, mode) }
}
