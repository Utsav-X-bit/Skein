//go:build android

package main

// The services-layer VPN bridge (gui/services/vpn.go): this file is
// the only place that calls the Go->Java JNI helpers in
// jni_android.go. It registers itself at startup so VpnService and
// the ServiceService Android overrides can drive the Java
// SkeinVpnService / VpnService.prepare() flow. A failed bridge call
// is logged, never fatal: the service methods that depend on it
// re-check live state afterwards, and the frontend shows the
// permission-denied state screen when the VPN cannot start.

import "C"

import (
	"log"

	"github.com/Utsav-X-bit/skein/gui/services"
	"github.com/Utsav-X-bit/skein/internal/link"
	"github.com/Utsav-X-bit/skein/internal/path"
)

// androidVpnBridge implements services' vpnBridge via JNI calls into
// MainActivity (cached by nativeInitSkeinBridge, called from
// MainActivity.onCreate; re-cached on every onCreate so a recreated
// Activity is the one that receives the calls).
type androidVpnBridge struct{}

func (androidVpnBridge) RequestPermission() {
	if rc := requestVpnPermission(); rc != 0 {
		log.Printf("skein: android: requestVpnPermission failed (rc=%d): bridge not ready", rc)
	}
}

func (androidVpnBridge) PermissionGranted() (bool, bool) {
	return vpnPermissionGranted()
}

func (androidVpnBridge) OpenSettings() {
	if rc := openVpnSettings(); rc != 0 {
		log.Printf("skein: android: openVpnSettings failed (rc=%d): bridge not ready", rc)
	}
}

func (androidVpnBridge) StartVpn() {
	// Clear before the hand-off: the Java service reports a failure
	// asynchronously (SkeinVpnService.nativeVpnFailure), and the Android
	// service helper polls VpnLastError while it waits for the connection.
	// Leaving the previous attempt's reason here would make a fresh start
	// fail instantly with a stale message.
	services.ClearVpnError()
	if rc := startVpnService(); rc != 0 {
		services.NoteVpnError("The VPN service could not be started (the app UI is not ready).")
		log.Printf("skein: android: startVpnService failed (rc=%d): bridge not ready", rc)
	}
}

func (androidVpnBridge) StopVpn() {
	if rc := stopVpnService(); rc != 0 {
		log.Printf("skein: android: stopVpnService failed (rc=%d): bridge not ready", rc)
	}
}

func (androidVpnBridge) SetHighPerformance(enabled bool) {
	if rc := setHighPerformanceWifi(enabled); rc != 0 {
		log.Printf("skein: android: setHighPerformanceWifi(%v) failed (rc=%d): bridge not ready", enabled, rc)
	}
}

func (androidVpnBridge) HighPerformanceEnabled() (bool, bool) {
	return highPerformanceWifiEnabled()
}

func init() {
	services.SetVpnBridge(androidVpnBridge{})
	// The daemon's underlay UDP socket must bypass the VPN (see
	// internal/link/listen_android.go); without this its packets to
	// a peer are routed into the TUN and the peer is never reached.
	link.ProtectSocket = protectSocketFD
	// net.Interfaces() is denied to Android apps, so internal/path cannot
	// enumerate the device's own addresses or its multicast-capable
	// interfaces and every node advertises no LAN candidate ("Could not
	// determine host IP addresses"). Java reads both from
	// ConnectivityManager instead, skipping our own VPN network.
	path.LocalAddresses = androidLocalAddresses
	path.LocalInterfaces = androidLocalInterfaces
}
