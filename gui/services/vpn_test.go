package services

import (
	"context"
	"testing"
)

// fakeVpnBridge stands in for the JNI bridge so the permission state
// machine and the asynchronous start error are testable off-device.
type fakeVpnBridge struct {
	granted    bool
	ok         bool
	starts     int
	stops      int
	highPerf   bool
	highPerfOk bool
}

func (f *fakeVpnBridge) RequestPermission() {}

func (f *fakeVpnBridge) PermissionGranted() (bool, bool) { return f.granted, f.ok }

func (f *fakeVpnBridge) OpenSettings() {}

func (f *fakeVpnBridge) StartVpn() { f.starts++ }

func (f *fakeVpnBridge) StopVpn() { f.stops++ }

func (f *fakeVpnBridge) SetHighPerformance(enabled bool) { f.highPerf = enabled }

func (f *fakeVpnBridge) HighPerformanceEnabled() (bool, bool) { return f.highPerf, f.highPerfOk }

// withVpnBridge installs b for the duration of the test and restores the
// package state afterwards (these are process-wide singletons).
func withVpnBridge(t *testing.T, b vpnBridge) {
	t.Helper()
	prevBridge, prevState, prevErr := vpnBridgeImpl, vpnStoredState(), VpnLastError()
	vpnBridgeImpl = b
	t.Cleanup(func() {
		vpnBridgeImpl = prevBridge
		setVpnStoredState(prevState)
		NoteVpnError(prevErr)
	})
}

func TestVpnStatusReportsRevokedPermission(t *testing.T) {
	withVpnBridge(t, &fakeVpnBridge{granted: false, ok: true})
	setVpnStoredState("granted")

	// The user revoked Skein in system Settings: reporting the stored
	// "granted" would skip the consent dialog on the next Connect.
	st, err := (&VpnService{}).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != "denied" {
		t.Fatalf("state = %q, want denied", st.State)
	}
	if got := vpnStoredState(); got != "denied" {
		t.Fatalf("stored state = %q, want denied", got)
	}
}

func TestVpnStatusKeepsStateWhenBridgeNotReady(t *testing.T) {
	withVpnBridge(t, &fakeVpnBridge{granted: false, ok: false})
	setVpnStoredState("granted")

	// ok=false means "could not ask Android" (no Activity yet), which is
	// not a denial and must not clear a recorded grant.
	st, err := (&VpnService{}).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != "granted" {
		t.Fatalf("state = %q, want granted", st.State)
	}
}

func TestVpnStatusTakesLiveGrant(t *testing.T) {
	withVpnBridge(t, &fakeVpnBridge{granted: true, ok: true})
	setVpnStoredState("unknown")

	st, err := (&VpnService{}).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != "granted" {
		t.Fatalf("state = %q, want granted", st.State)
	}
}

func TestVpnStartClearsPreviousError(t *testing.T) {
	b := &fakeVpnBridge{granted: true, ok: true}
	withVpnBridge(t, b)
	NoteVpnError("the mesh did not connect")

	if err := (&VpnService{}).Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := VpnLastError(); got != "" {
		t.Fatalf("VpnLastError = %q, want empty after a new start", got)
	}
	if b.starts != 1 {
		t.Fatalf("StartVpn calls = %d, want 1", b.starts)
	}
}

func TestVpnDenialRecordsStartError(t *testing.T) {
	withVpnBridge(t, &fakeVpnBridge{granted: false, ok: true})

	NoteVpnPermissionResult(false)
	if got := VpnLastError(); got == "" {
		t.Fatal("VpnLastError is empty after a denial; a start waiting on consent would time out with no reason")
	}
	if got := vpnStoredState(); got != "denied" {
		t.Fatalf("state = %q, want denied", got)
	}

	NoteVpnPermissionResult(true)
	if got := vpnStoredState(); got != "granted" {
		t.Fatalf("state = %q, want granted", got)
	}
}

func TestHighPerformanceWifiRoundTripsThroughTheBridge(t *testing.T) {
	b := &fakeVpnBridge{highPerfOk: true}
	withVpnBridge(t, b)

	// Off by default: the lock costs battery, so nothing may enable it.
	enabled, err := (&VpnService{}).HighPerformanceWifi()
	if err != nil {
		t.Fatalf("HighPerformanceWifi: %v", err)
	}
	if enabled {
		t.Fatal("high-performance Wi-Fi is on before the user asked for it")
	}

	if err := (&VpnService{}).SetHighPerformanceWifi(true); err != nil {
		t.Fatalf("SetHighPerformanceWifi: %v", err)
	}
	if !b.highPerf {
		t.Fatal("the bridge was not told to enable the lock")
	}
	enabled, err = (&VpnService{}).HighPerformanceWifi()
	if err != nil || !enabled {
		t.Fatalf("HighPerformanceWifi = %v, %v; want true, nil", enabled, err)
	}

	if err := (&VpnService{}).SetHighPerformanceWifi(false); err != nil {
		t.Fatalf("SetHighPerformanceWifi(false): %v", err)
	}
	if b.highPerf {
		t.Fatal("the bridge was not told to release the lock")
	}
}
