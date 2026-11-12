//go:build !linux && !windows && !android

package firewall

import "time"

func ensure(Config) error { return nil }
func remove(Config) error { return nil }

func Manager() string { return "" }

func ensurePort(Config) error      { return nil }
func ensureInterface(Config) error { return nil }

func status(cfg Config) (Status, error) {
	return Status{LastCheckMS: time.Now().UnixMilli(), State: "unknown", Rules: expectedRules(cfg)}, nil
}

func repair(cfg Config) (Status, error) { return status(cfg) }

func expectedRules(cfg Config) []Rule {
	return []Rule{{Name: "underlay", Direction: "in", Action: "allow", Protocol: "udp", Port: "0"}}
}
