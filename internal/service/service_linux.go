//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const unitPath = "/etc/systemd/system/skein.service"

func install(executable, configPath string) error {
	if !strings.HasPrefix(executable, "/") {
		return fmt.Errorf("service: executable path must be absolute, got %q", executable)
	}
	if !strings.HasPrefix(configPath, "/") {
		return fmt.Errorf("service: config path must be absolute, got %q", configPath)
	}
	unit := fmt.Sprintf(`[Unit]
Description=Skein mesh VPN
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s run -c %s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`, executable, configPath)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		os.Remove(unitPath)
		return fmt.Errorf("service: daemon-reload: %w: %s", err, out)
	}
	if out, err := exec.Command("systemctl", "enable", Name+".service").CombinedOutput(); err != nil {
		os.Remove(unitPath)
		_ = exec.Command("systemctl", "daemon-reload").Run()
		return fmt.Errorf("service: enable: %w: %s", err, out)
	}
	return nil
}

func uninstall() error {
	_ = exec.Command("systemctl", "disable", "--now", Name+".service").Run()
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("service: daemon-reload: %w: %s", err, out)
	}
	return nil
}
