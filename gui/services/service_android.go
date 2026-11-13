//go:build android

package services

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		// No configuration means there is no network to run, whatever the
		// daemon object still holds. After a reset the config is removed
		// before the service stops, so reporting "running" here left the
		// shell on a paused mesh screen with no way back to setup.
		if !android.ConfigExists() {
			return service.Status{State: service.StateMissing}, nil
		}
		if android.Started() {
			return service.Status{State: service.StateRunning}, nil
		}
		return service.Status{State: service.StateStopped}, nil
	}
	runHelper = func(ctx context.Context, verb string, args ...string) error {
		// A start waits for the tunnel to come up, which can take a while;
		// a stop is local teardown and must not freeze the caller for the
		// same window.
		timeout := 40 * time.Second
		if verb == "stop" {
			timeout = 12 * time.Second
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		_ = args
		if vpnBridgeImpl == nil {
			return errors.New("android: the VPN bridge is not ready yet; restart the app and retry")
		}
		switch verb {
		case "start":
			vpnBridgeImpl.StartVpn()
			return waitAndroidVPN(ctx, true)
		case "stop":
			vpnBridgeImpl.StopVpn()
			return waitAndroidVPN(ctx, false)
		case "restart":
			vpnBridgeImpl.StopVpn()
			if err := waitAndroidVPN(ctx, false); err != nil {
				return err
			}
			vpnBridgeImpl.StartVpn()
			return waitAndroidVPN(ctx, true)
		case "install", "uninstall", "enable", "disable":
			return fmt.Errorf("android: %s is not available: Skein runs as the Android VPN service, there is nothing to install", verb)
		default:
			return fmt.Errorf("android: helper verb %q is not available on Android", verb)
		}
	}
}

// Java starts and stops the foreground service asynchronously. Do not hand
// the desktop reconciler a terminal "stopped" snapshot before the work begins.
func waitAndroidVPN(ctx context.Context, connected bool) error {
	ticker := time.NewTicker(lifecyclePoll)
	defer ticker.Stop()
	for {
		st := android.Connection()
		if connected && android.Started() && st.State == "connected" {
			return nil
		}
		if !connected && !android.Started() {
			return nil
		}
		if connected {
			if err := VpnLastError(); err != "" {
				return errors.New(err)
			}
			if android.Started() && st.State == "error" && st.LastError != "" {
				return errors.New(st.LastError)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("android: VPN did not reach the requested state: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
