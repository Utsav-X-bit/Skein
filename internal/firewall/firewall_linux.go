//go:build linux

package firewall

import (
	"fmt"
	"strings"
)

func ensure(cfg Config) error {
	if ufwActive() {
		if _, err := runner("ufw", "allow", fmt.Sprintf("%d/udp", cfg.Port)); err != nil {
			return err
		}
		if _, err := runner("ufw", "allow", "in", "on", cfg.Interface); err != nil {
			return err
		}
		return nil
	}
	if firewalldActive() {
		if _, err := runner("firewall-cmd", "--permanent", fmt.Sprintf("--add-port=%d/udp", cfg.Port)); err != nil {
			return err
		}
		if _, err := runner("firewall-cmd", "--permanent", "--zone=trusted", "--add-interface="+cfg.Interface); err != nil {
			return err
		}
		_, err := runner("firewall-cmd", "--reload")
		return err
	}
	// No managed firewall detected; nothing to open.
	return nil
}

func remove(cfg Config) error {
	if ufwActive() {
		_, _ = runner("ufw", "delete", "allow", fmt.Sprintf("%d/udp", cfg.Port))
		_, _ = runner("ufw", "delete", "allow", "in", "on", cfg.Interface)
		return nil
	}
	if firewalldActive() {
		_, _ = runner("firewall-cmd", "--permanent", fmt.Sprintf("--remove-port=%d/udp", cfg.Port))
		_, _ = runner("firewall-cmd", "--permanent", "--zone=trusted", "--remove-interface="+cfg.Interface)
		_, err := runner("firewall-cmd", "--reload")
		return err
	}
	return nil
}

func ufwActive() bool {
	out, err := runner("ufw", "status")
	return err == nil && strings.Contains(out, "Status: active")
}

func firewalldActive() bool {
	out, err := runner("firewall-cmd", "--state")
	return err == nil && strings.Contains(out, "running")
}
