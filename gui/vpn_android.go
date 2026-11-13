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
)

// androidVpnBridge implements services' vpnBridge via JNI calls into
// MainActivity (cached by nativeInitSkeinBridge, called from
// MainActivity.onCreate).
type androidVpnBridge struct{}

func (androidVpnBridge) RequestPermission() {
	if rc := requestVpnPermission(); rc != 0 {
		log.Printf("skein: android: requestVpnPermission failed (rc=%d): bridge not ready", rc)
	}
}

func (androidVpnBridge) PermissionGranted() bool {
	granted, ok := vpnPermissionGranted()
	if !ok {
		return false
	}
	return granted
}

func (androidVpnBridge) OpenSettings() {
	if rc := openVpnSettings(); rc != 0 {
		log.Printf("skein: android: openVpnSettings failed (rc=%d): bridge not ready", rc)
	}
}

func (androidVpnBridge) StartVpn() {
	if rc := startVpnService(); rc != 0 {
		log.Printf("skein: android: startVpnService failed (rc=%d): bridge not ready", rc)
	}
}

func (androidVpnBridge) StopVpn() {
	if rc := stopVpnService(); rc != 0 {
		log.Printf("skein: android: stopVpnService failed (rc=%d): bridge not ready", rc)
	}
}

func init() {
	services.SetVpnBridge(androidVpnBridge{})
	// The daemon's underlay UDP socket must bypass the VPN (see
	// internal/link/listen_android.go); without this its packets to
	// a peer are routed into the TUN and the peer is never reached.
	link.ProtectSocket = protectSocketFD
}
