//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

func requirePrivilege(needed bool) error {
	if !needed {
		return nil
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	if token.IsElevated() {
		return nil
	}
	return errors.New("run elevated (UAC): the config and identity are owned by SYSTEM")
}
