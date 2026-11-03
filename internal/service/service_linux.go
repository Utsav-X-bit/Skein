//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const unitPath = "/etc/systemd/system/skein.service"

func unitFor(executable, configPath string) (string, error) {
	if !strings.HasPrefix(executable, "/") {
		return "", fmt.Errorf("service: executable path must be absolute, got %q", executable)
	}
	if !strings.HasPrefix(configPath, "/") {
		return "", fmt.Errorf("service: config path must be absolute, got %q", configPath)
	}
	execArg, err := unitArg(executable)
	if err != nil {
		return "", err
	}
	cfgArg, err := unitArg(configPath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`[Unit]
Description=Skein mesh VPN
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s run -c %s
Restart=on-failure
RestartSec=2
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
StateDirectory=skein
RuntimeDirectory=skein
ReadWritePaths=/etc/skein /var/lib/skein /run/skein -/etc/ufw -/etc/firewalld -/run/ufw -/run/xtables.lock
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK

[Install]
WantedBy=multi-user.target
`, execArg, cfgArg), nil
}

func install(executable, configPath string) error {
	unit, err := unitFor(executable, configPath)
	if err != nil {
		return err
	}
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

func rewrite(executable, configPath string) error {
	if _, err := os.Stat(unitPath); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	unit, err := unitFor(executable, configPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("service: daemon-reload: %w: %s", err, out)
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

func start() error {
	return systemctl("start")
}

func stop() error {
	return systemctl("stop")
}

func restart() error {
	return systemctl("restart")
}

func systemctl(action string) error {
	out, err := exec.Command("systemctl", action, Name+".service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("service: systemctl %s: %w: %s", action, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// status reads the unit state and enabled flag without elevation.
func status() (Status, error) {
	if _, err := os.Stat(unitPath); err != nil {
		if os.IsNotExist(err) {
			return Status{State: StateMissing}, nil
		}
		return Status{}, err
	}
	st := Status{Enabled: enabled()}
	out, err := exec.Command("systemctl", "is-active", Name+".service").Output()
	if err != nil {
		// is-active exits nonzero for every non-active state; the state is
		// still printed, so a command failure means it truly is unreadable.
		if strings.TrimSpace(string(out)) == "" {
			return Status{}, fmt.Errorf("service: systemctl is-active: %w", err)
		}
	}
	switch strings.TrimSpace(string(out)) {
	case "active":
		st.State = StateRunning
	case "activating", "auto-restart":
		st.State = StateStarting
	case "deactivating":
		st.State = StateStopping
	case "failed":
		st.State = StateError
	case "inactive", "dead":
		st.State = StateStopped
	default:
		st.State = StateUnknown
	}
	return st, nil
}

// enabled reports whether the unit starts at boot, without elevation.
func enabled() bool {
	out, err := exec.Command("systemctl", "is-enabled", Name+".service").Output()
	return err == nil && strings.TrimSpace(string(out)) == "enabled"
}

// installed parses the unit's ExecStart into its executable and config path.
func installed() (string, string, bool, error) {
	raw, err := os.ReadFile(unitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		args, err := splitUnitLine(strings.TrimPrefix(line, "ExecStart="))
		if err != nil {
			return "", "", false, fmt.Errorf("service: unit ExecStart: %w", err)
		}
		if len(args) < 2 {
			return "", "", false, fmt.Errorf("service: unit ExecStart has %d arguments, want at least 2", len(args))
		}
		for i, arg := range args {
			if arg == "-c" && i+1 < len(args) {
				return args[0], args[i+1], true, nil
			}
		}
		return "", "", false, fmt.Errorf("service: unit ExecStart has no -c config path")
	}
	return "", "", false, fmt.Errorf("service: unit has no ExecStart line")
}

// splitUnitLine splits a systemd command line on whitespace, honoring double
// quotes and systemd's backslash and %% escapes.
func splitUnitLine(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			switch c {
			case '"':
				inQuote = false
			case '\\':
				i++
				if i >= len(s) {
					return nil, fmt.Errorf("trailing backslash")
				}
				cur.WriteByte(s[i])
			default:
				cur.WriteByte(c)
			}
		case c == '"':
			inQuote = true
		case c == '\\':
			i++
			if i >= len(s) {
				return nil, fmt.Errorf("trailing backslash")
			}
			cur.WriteByte(s[i])
		case c == '%':
			if i+1 < len(s) && s[i+1] == '%' {
				i++
			}
			cur.WriteByte('%')
		case c == ' ' || c == '\t':
			if cur.Len() > 0 {
				args = append(args, cur.String())
			}
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quote")
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args, nil
}

// unitArg quotes one systemd ExecStart argument and rejects control characters
// that would inject unit directives.
func unitArg(s string) (string, error) {
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", fmt.Errorf("service: path %q contains a control character", s)
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(s)
	return `"` + escaped + `"`, nil
}
