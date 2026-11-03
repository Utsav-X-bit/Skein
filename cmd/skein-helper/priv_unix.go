//go:build !windows

package main

import (
	"errors"
	"os"
)

func requirePrivilege(needed bool) error {
	if !needed || os.Geteuid() == 0 {
		return nil
	}
	return errors.New("run elevated (sudo skein-helper ...): the config and identity are root-owned")
}
