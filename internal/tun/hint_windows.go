//go:build windows

package tun

import "fmt"

// hint explains the most common Windows startup failure.
func hint(err error) error {
	return fmt.Errorf("%w (wintun.dll must sit next to the executable; "+
		"run scripts/fetch-wintun.ps1 or copy it from the release archive)", err)
}
