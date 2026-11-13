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
	// vpnLastError is the reason the most recent start attempt failed,
	// reported asynchronously from the Java service (the connect runs off
	// the caller's thread, so the failure cannot be a return value). The
	// Android service helper waits for the connection and returns this
	// when the wait times out, so the UI shows the real cause.
	vpnLastError string
)

// vpnBridge is the Java-side VPN control, implemented in package main
// (gui/vpn_android.go) and registered at Android startup. The
// services layer never touches JNI directly.
type vpnBridge interface {
	RequestPermission()
	// PermissionGranted reports the live system state. ok is false when
	// the bridge could not be asked (no Activity yet), which is not the
	// same as "denied".
	PermissionGranted() (granted, ok bool)
	OpenSettings()
	StartVpn()
	StopVpn()
	// SetHighPerformance persists and applies the high-performance Wi-Fi
	// preference: the radio stays awake while the tunnel is up, removing the
	// power-save delay on inbound packets at the cost of battery.
	SetHighPerformance(enabled bool)
	// HighPerformanceEnabled reports the persisted preference. ok is false
	// when the bridge cannot be asked, which is not the same as "off".
	HighPerformanceEnabled() (enabled, ok bool)
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

// NoteVpnError records why the current VPN start attempt failed. It is
// called from the JNI callback in package main
// (SkeinVpnService.nativeVpnFailure) and, on a denial, from
// NoteVpnPermissionResult. The start itself is asynchronous (the Java
// service runs the connect on its own thread), so the Android service
// helper polls VpnLastError while it waits for the connection.
func NoteVpnError(reason string) {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	vpnLastError = reason
}

// ClearVpnError forgets the previous failure. Called synchronously when a
// start is requested, before the JNI hand-off, so a waiter can never read
// the reason from the attempt before it.
func ClearVpnError() {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	vpnLastError = ""
}

// VpnLastError returns the reason the most recent start attempt failed, or
// "" when none was reported.
func VpnLastError() string {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	return vpnLastError
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
	} else {
		// A start that was waiting on the dialog ends here; give the
		// service helper a reason to return instead of timing out.
		NoteVpnError("VPN permission was denied")
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
	if vpnBridgeImpl != nil {
		granted, ok := vpnBridgeImpl.PermissionGranted()
		switch {
		case granted:
			state = "granted"
			setVpnStoredState(state)
		case ok && state == "granted":
			// Android says not granted although we recorded a grant:
			// the user revoked it in system Settings. Reporting the stale
			// "granted" would skip the consent flow on the next Connect.
			state = "denied"
			setVpnStoredState(state)
		}
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
	// Before the hand-off: the bridge clears it again right before the
	// JNI call, so a waiter can never read the previous attempt's reason.
	ClearVpnError()
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

// HighPerformanceWifi reports whether the app holds a high-performance Wi-Fi
// lock while the tunnel is up. It is off by default: the lock keeps the radio
// awake, which removes Android's power-save delay on inbound packets but costs
// battery. Desktop builds report false — there is no such lock to hold.
func (s *VpnService) HighPerformanceWifi() (bool, error) {
	if vpnBridgeImpl == nil {
		return false, nil
	}
	enabled, ok := vpnBridgeImpl.HighPerformanceEnabled()
	if !ok {
		// The bridge is not ready yet (no Activity); report the stored
		// default rather than inventing a value.
		return false, nil
	}
	return enabled, nil
}

// SetHighPerformanceWifi stores the preference and applies it to a running
// tunnel immediately, so the user does not have to reconnect.
func (s *VpnService) SetHighPerformanceWifi(enabled bool) error {
	if vpnBridgeImpl == nil {
		return errors.New("Wi-Fi performance control is only available on Android")
	}
	vpnBridgeImpl.SetHighPerformance(enabled)
	return nil
}
