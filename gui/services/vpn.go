package services

import (
	"context"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// VpnService is the Android VPN permission bridge (design doc B1).
// Desktop builds have no VPN permission flow: every method reports
// that, so the mobile shell — the only caller — never runs on a
// platform without the system VPN dialog.
//
// The flow, from the frontend's point of view:
//  1. Status() reports granted / denied / unknown.
//  2. RequestPermission() asks the Java side to launch
//     VpnService.prepare(). It returns at once: the system dialog is
//     asynchronous, and the outcome arrives as the EventVpnPermission
//     Wails event (and is then visible from Status()).
//  3. On granted, the caller starts the service (ServiceService.Start
//     maps to the VpnService start on Android); on denied it shows
//     the permission-denied state screen, whose OpenVpnSettings()
//     opens the system VPN settings.
//
// The Go side never touches Android APIs directly: the Java calls go
// through the bridge registered by package main at Android startup
// (gui/vpn_android.go), which is the only file that speaks JNI.
type VpnService struct{}

// VpnPermission is the vpn_permission state the design doc specifies.
type VpnPermission struct {
	State     string `json:"state"` // "granted" | "denied" | "unknown"
	VpnActive bool   `json:"vpn_active"`
}

// VpnPermissionEvent is the payload of EventVpnPermission.
type VpnPermissionEvent struct {
	Granted bool   `json:"granted"`
	State   string `json:"state"`
}

// EventVpnPermission fires when the system VPN consent dialog is
// answered (or when prepare() reports permission already granted).
const EventVpnPermission = "vpn:permission"

var (
	vpnMu    sync.Mutex
	vpnState = "unknown"
)

// vpnBridge is the Java-side VPN control, implemented in package main
// (gui/vpn_android.go) and registered at Android startup. The
// services layer never touches JNI directly.
type vpnBridge interface {
	RequestPermission()
	PermissionGranted() bool
	OpenSettings()
	StartVpn()
	StopVpn()
}

var vpnBridgeImpl vpnBridge

// SetVpnBridge registers the Java-side VPN control. Called once from
// package main on Android; never called on desktop.
func SetVpnBridge(b vpnBridge) { vpnBridgeImpl = b }

func vpnStoredState() string {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	return vpnState
}

func setVpnStoredState(state string) {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	vpnState = state
}

// NoteVpnPermissionResult records the outcome of the system VPN
// consent dialog. It is called from the JNI callback in package main
// (MainActivity.onActivityResult), never from the frontend. It also
// emits EventVpnPermission so the Connect flow can proceed without
// polling.
func NoteVpnPermissionResult(granted bool) {
	state := "denied"
	if granted {
		state = "granted"
	}
	setVpnStoredState(state)
	if app := application.Get(); app != nil {
		app.Event.Emit(EventVpnPermission, VpnPermissionEvent{Granted: granted, State: state})
	}
}

// Status returns the VPN permission state. When the bridge is
// present it asks Android live (VpnService.prepare() returns null
// when already authorised), so a grant made in system Settings is
// picked up without restarting the app.
func (s *VpnService) Status(context.Context) (VpnPermission, error) {
	state := vpnStoredState()
	if vpnBridgeImpl != nil && vpnBridgeImpl.PermissionGranted() {
		state = "granted"
		setVpnStoredState(state)
	}
	return VpnPermission{State: state, VpnActive: vpnActive()}, nil
}

// RequestPermission launches the system VPN consent dialog via the
// Java side. It returns as soon as the request is handed over; the
// outcome arrives as EventVpnPermission.
func (s *VpnService) RequestPermission(context.Context) error {
	if vpnBridgeImpl == nil {
		return errors.New("VPN permission is only available on Android")
	}
	vpnBridgeImpl.RequestPermission()
	return nil
}

// OpenVpnSettings opens the system VPN settings screen, so a user
// who denied permission can re-enable Skein there.
func (s *VpnService) OpenVpnSettings(context.Context) error {
	if vpnBridgeImpl == nil {
		return errors.New("VPN settings are only available on Android")
	}
	vpnBridgeImpl.OpenSettings()
	return nil
}

// Start starts the SkeinVpnService (which starts the in-process
// daemon, builds the TUN and connects). It is the same action
// ServiceService.Start performs on Android; it is exposed here so
// the Connect flow can start the VPN without going through the
// service-mutation reconciler.
func (s *VpnService) Start(context.Context) error {
	if vpnBridgeImpl == nil {
		return errors.New("the Android VPN service is only available on Android")
	}
	vpnBridgeImpl.StartVpn()
	return nil
}

// Stop stops the SkeinVpnService (disconnects the dataplane and
// stops the in-process daemon).
func (s *VpnService) Stop(context.Context) error {
	if vpnBridgeImpl == nil {
		return errors.New("the Android VPN service is only available on Android")
	}
	vpnBridgeImpl.StopVpn()
	return nil
}
