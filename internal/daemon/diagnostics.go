package daemon

import (
	"runtime"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/version"
)

// Diagnostics implements api.GUI: the redacted support bundle. It contains
// the log tail, peer/route tables, the redacted config view, and platform
// facts — never the raw config file, secrets, or join strings.
func (d *Daemon) Diagnostics() api.DiagnosticBundle {
	logs, _ := d.logs.tail(0, logRingSize)
	if logs == nil {
		logs = []api.LogEntry{}
	}
	peers := d.Peers()
	if peers == nil {
		peers = []api.Peer{}
	}
	routes := d.Routes()
	if routes == nil {
		routes = []api.Route{}
	}
	return api.DiagnosticBundle{
		GeneratedMS: time.Now().UnixMilli(),
		Version:     version.Info(),
		Protocol:    version.Protocol,
		Platform:    runtime.GOOS + "/" + runtime.GOARCH,
		Logs:        logs,
		Peers:       peers,
		Routes:      routes,
		Config:      d.Config(),
		Redactions:  d.redact.list(),
	}
}

// Logs implements api.GUI.
func (d *Daemon) Logs(since uint64, limit int) ([]api.LogEntry, uint64, error) {
	entries, next := d.logs.tail(since, limit)
	if entries == nil {
		entries = []api.LogEntry{}
	}
	return entries, next, nil
}
