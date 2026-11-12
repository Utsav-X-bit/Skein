//go:build android

package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/Utsav-X-bit/skein/internal/android"
	"github.com/Utsav-X-bit/skein/internal/service"
)

// managerName reports the Android VPN service as the service manager:
// on Android the "service" is SkeinVpnService, not systemd.
func managerName() string { return "android-vpn" }

func init() {
	// On Android there is no systemd/SCM and no helper binary: the
	// service lifecycle is the VpnService start/stop, driven through
	// the JNI bridge registered by package main. Status reads the
	// in-process daemon plus the on-disk config, so the shell's
	// onboarding gate and the daemon-down state screen see truth:
	// missing = no network configured yet, stopped = configured but
	// the VPN is down, running = daemon up.
	readServiceStatus = func() (service.Status, error) {
		if android.Started() {
			return service.Status{State: service.StateRunning}, nil
		}
		if android.ConfigExists() {
			return service.Status{State: service.StateStopped}, nil
		}
		return service.Status{State: service.StateMissing}, nil
	}
	runHelper = func(ctx context.Context, verb string, args ...string) error {
		_ = ctx
		_ = args
		if vpnBridgeImpl == nil {
			return errors.New("android: the VPN bridge is not ready yet; restart the app and retry")
		}
		switch verb {
		case "start":
			vpnBridgeImpl.StartVpn()
			return nil
		case "stop":
			vpnBridgeImpl.StopVpn()
			return nil
		case "restart":
			vpnBridgeImpl.StopVpn()
			vpnBridgeImpl.StartVpn()
			return nil
		case "install", "uninstall", "enable", "disable":
			return fmt.Errorf("android: %s is not available: Skein runs as the Android VPN service, there is nothing to install", verb)
		default:
			return fmt.Errorf("android: helper verb %q is not available on Android", verb)
		}
	}
}
