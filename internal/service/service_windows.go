//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func binPathFor(executable, configPath string) (string, error) {
	if !strings.Contains(executable, `:\`) {
		return "", fmt.Errorf("service: executable path must be absolute, got %q", executable)
	}
	if !strings.Contains(configPath, `:\`) {
		return "", fmt.Errorf("service: config path must be absolute, got %q", configPath)
	}
	return fmt.Sprintf(`"%s" run -c "%s"`, executable, configPath), nil
}

func install(executable, configPath string) error {
	binPath, err := binPathFor(executable, configPath)
	if err != nil {
		return err
	}
	if out, err := exec.Command("sc", "create", Name, "binPath=", binPath, "start=", "auto", "DisplayName=", "Skein mesh VPN").CombinedOutput(); err != nil {
		return fmt.Errorf("service: sc create: %w: %s", err, out)
	}
	if out, err := exec.Command("sc", "description", Name, "Skein mesh VPN daemon").CombinedOutput(); err != nil {
		return fmt.Errorf("service: sc description: %w: %s", err, out)
	}
	// Recover from crashes: retry at 5s, 10s, 30s, then give up.
	if out, err := exec.Command("sc", "failure", Name, "reset=86400",
		"actions=restart/5000/restart/10000/restart/30000").CombinedOutput(); err != nil {
		return fmt.Errorf("service: sc failure: %w: %s", err, out)
	}
	return nil
}

func rewrite(executable, configPath string) error {
	if _, err := exec.Command("sc", "qc", Name).Output(); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	binPath, err := binPathFor(executable, configPath)
	if err != nil {
		return err
	}
	// sc change rejects a new binPath while the service is running.
	if out, err := exec.Command("sc", "change", Name, "binPath="+binPath).CombinedOutput(); err != nil {
		return fmt.Errorf("service: sc change: %w: %s", err, out)
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

func start() error {
	if out, err := exec.Command("sc", "start", Name).CombinedOutput(); err != nil {
		return fmt.Errorf("service: sc start: %w: %s", err, out)
	}
	return nil
}

func stop() error {
	if out, err := exec.Command("sc", "stop", Name).CombinedOutput(); err != nil {
		// 1062: already stopped.
		if strings.Contains(string(out), "1062") {
			return nil
		}
		return fmt.Errorf("service: sc stop: %w: %s", err, out)
	}
	return nil
}

func restart() error {
	if err := stop(); err != nil {
		return err
	}
	return start()
}

// status reads the SCM state without elevation (sc query is readable by any
// user).
func status() (Status, error) {
	out, err := exec.Command("sc", "query", Name).CombinedOutput()
	if err != nil {
		msg := strings.ToLower(err.Error() + " " + string(out))
		if strings.Contains(msg, "1060") || strings.Contains(msg, "does not exist as an installed service") {
			return Status{State: StateMissing}, nil
		}
		return Status{}, fmt.Errorf("service: sc query: %w: %s", err, out)
	}
	st := Status{State: StateUnknown}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "STATE") {
			fields := strings.Fields(line)
			// "STATE : 4  RUNNING"
			for i := 1; i+1 < len(fields); i++ {
				n, err := strconv.Atoi(fields[i])
				if err != nil {
					continue
				}
				switch n {
				case 1:
					st.State = StateStopped
				case 2:
					st.State = StateStarting
				case 3:
					st.State = StateStopping
				case 4:
					st.State = StateRunning
				default:
					st.State = StateUnknown
				}
			}
		}
	}
	st.Enabled = enabled()
	return st, nil
}

// enabled reports the service start type without elevation.
func enabled() bool {
	out, err := exec.Command("sc", "qc", Name).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToUpper(line), "AUTO_START") {
			return true
		}
	}
	return false
}

// installed parses the SCM binPath into its executable and config path.
func installed() (string, string, bool, error) {
	out, err := exec.Command("sc", "qc", Name).Output()
	if err != nil {
		return "", "", false, nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		upper := strings.ToUpper(line)
		idx := strings.Index(upper, "BINPATH")
		if idx < 0 {
			continue
		}
		rest := line[idx+len("BINPATH"):]
		rest = strings.TrimLeft(rest, " =:")
		args, err := splitQuoted(rest)
		if err != nil {
			return "", "", false, fmt.Errorf("service: binPath: %w", err)
		}
		for i, arg := range args {
			if arg == "-c" && i+1 < len(args) {
				return args[0], args[i+1], true, nil
			}
		}
		return "", "", false, fmt.Errorf("service: binPath has no -c config path")
	}
	return "", "", false, fmt.Errorf("service: binPath not found")
}

// splitQuoted splits a binPath on whitespace, keeping double-quoted runs
// intact.
func splitQuoted(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case (c == ' ' || c == '\t') && !inQuote:
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
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
