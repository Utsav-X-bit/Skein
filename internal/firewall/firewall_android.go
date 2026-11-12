//go:build android

package firewall

import "time"

// Android has no ufw, firewalld, or iptables for apps: routing is owned
// by the Android VpnService (SkeinVpnService), which routes the overlay
// prefixes through the TUN while the VPN is active. The firewall
// package interface is therefore a no-op that succeeds on Android;
// the actual "firewall" behaviour — which traffic goes through the
// TUN — is configured in the VpnService.Builder (addRoute for the
// overlay prefixes, allowBypass for other apps).

func ensure(Config) error          { return nil }
func ensurePort(Config) error      { return nil }
func ensureInterface(Config) error { return nil }
func remove(Config) error          { return nil }

// Manager reports the Android VPN service as the firewall backend.
func Manager() string { return "android-vpn" }

// expectedRules is empty on Android: there are no managed rules to
// list, because the VpnService owns routing (see the package note).
func expectedRules(Config) []Rule { return nil }

func status(Config) (Status, error) {
	return Status{
		Manager:     Manager(),
		State:       "managed",
		Rules:       nil,
		LastCheckMS: time.Now().UnixMilli(),
		Note:        "routing is handled by the Android VpnService",
	}, nil
}

func repair(cfg Config) (Status, error) { return status(cfg) }
