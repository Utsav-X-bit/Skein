//go:build android

package services

// managerName reports the Android VPN service as the service manager:
// on Android the "service" is SkeinVpnService, not systemd.
func managerName() string { return "android-vpn" }
