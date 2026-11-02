//go:build !linux && !windows

package firewall

func ensure(Config) error { return nil }
func remove(Config) error { return nil }

func Manager() string { return "" }

func ensurePort(Config) error      { return nil }
func ensureInterface(Config) error { return nil }
