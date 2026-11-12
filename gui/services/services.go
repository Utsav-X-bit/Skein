// Package services exposes the daemon API to the Wails frontend as typed,
// bound services. Each service maps to a group of /api/v1 endpoints; the
// frontend never talks to the daemon directly.
//
// W4 stubs: every method is typed against internal/api but returns
// ErrDaemonNotConnected instead of fabricating data. The transport that
// dials the control socket/pipe lands with the IPC work (W1/W2).
package services

import (
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ErrDaemonNotConnected is returned until the control socket/pipe transport
// is wired in. The frontend maps it to the "daemon offline" state.
var ErrDaemonNotConnected = errors.New("daemon not connected")

// UpdateInfo describes an available GUI update.
type UpdateInfo struct {
	Version string `json:"version"`
	Notes   string `json:"notes,omitempty"`
}

// HistoryRange selects a bounded activity window.
type HistoryRange struct {
	PeerID   string `json:"peer_id,omitempty"`
	Minutes  int    `json:"minutes"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Interval int    `json:"interval_s,omitempty"`
	RetainMB int    `json:"retain_mb,omitempty"`
}

// HistorySummary reports the GUI-owned bounded history state.
type HistorySummary struct {
	Enabled      bool   `json:"enabled"`
	IntervalS    int    `json:"interval_s"`
	RetainMB     int    `json:"retain_mb"`
	StoredBytes  uint64 `json:"stored_bytes"`
	StoredPoints uint64 `json:"stored_points"`
}

// AllServices returns the bound service set for the application.
func AllServices() []application.Service {
	return []application.Service{
		application.NewService(&AppService{}),
		application.NewService(&ConnectionService{}),
		application.NewService(&PeerService{}),
		application.NewService(&InviteService{}),
		application.NewService(&NetworkService{}),
		application.NewService(&ConfigService{}),
		application.NewService(&FirewallService{}),
		application.NewService(&DiagnosticsService{}),
		application.NewService(&ServiceService{}),
		application.NewService(&UpdateService{}),
		application.NewService(&HistoryService{}),
		application.NewService(&SetupService{}),
		application.NewService(&SecurityService{}),
		application.NewService(&IdentityService{}),
		application.NewService(&VpnService{}),
	}
}
