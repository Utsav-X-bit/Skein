//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strings"
)

func install(executable, configPath string) error {
	if !strings.Contains(executable, `:\`) {
		return fmt.Errorf("service: executable path must be absolute, got %q", executable)
	}
	binPath := fmt.Sprintf(`"%s" run -c "%s"`, executable, configPath)
	if out, err := exec.Command("sc", "create", Name, "binPath=", binPath, "start=", "auto", "DisplayName=", "Skein mesh VPN").CombinedOutput(); err != nil {
		return fmt.Errorf("service: sc create: %w: %s", err, out)
	}
	if out, err := exec.Command("sc", "description", Name, "Skein mesh VPN daemon").CombinedOutput(); err != nil {
		return fmt.Errorf("service: sc description: %w: %s", err, out)
	}
	return nil
}

func uninstall() error {
	out, err := exec.Command("sc", "delete", Name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "1060") {
		return fmt.Errorf("service: sc delete: %w: %s", err, out)
	}
	return nil
}
