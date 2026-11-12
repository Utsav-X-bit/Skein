package services

import (
	"bufio"
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/Utsav-X-bit/skein/internal/api"
)

const (
	EventConnectionChanged = "connection:changed"
	EventPeersChanged      = "peers:changed"
	EventRoutesChanged     = "routes:changed"
	EventActivitySample    = "activity:sample"
	EventLogEntry          = "log:entry"
	EventServiceChanged    = "service:changed"
	EventUpdateAvailable   = "update:available"
	EventPeerProbe         = "peer:probe"

	EventPeerUp                   = "peer_up"
	EventPeerJoined               = "peer_joined"
	EventPeerDiscovered           = "peer_discovered"
	EventPeerAdded                = "peer_added"
	EventPeerRemoved              = "peer_removed"
	EventPeerDown                 = "peer_down"
	EventPeerDeparted             = "peer_departed"
	EventDialerFallback           = "dialer_fallback"
	EventPeerLeave                = "peer:leave"
	EventPeerRejoined             = "peer:rejoined"
	EventNameChanged              = "name_changed"
	EventSecretRotated            = "secret_rotated"
	EventSystemReset              = "system_reset"
	EventSystemImported           = "system_imported"
	EventLeaveDataplaneStopFailed = "leave_dataplane_stop_failed"
)

// lifecycleEvent is the allowlist of daemon events that carry an api.Event
// envelope. They are forwarded verbatim so the frontend's session feed can
// show what happened; an unknown type is dropped rather than guessed at.
var lifecycleEvent = map[string]bool{
	EventPeerUp:                   true,
	EventPeerJoined:               true,
	EventPeerDiscovered:           true,
	EventPeerAdded:                true,
	EventPeerRemoved:              true,
	EventPeerDown:                 true,
	EventPeerDeparted:             true,
	EventDialerFallback:           true,
	EventPeerLeave:                true,
	EventPeerRejoined:             true,
	EventNameChanged:              true,
	EventSecretRotated:            true,
	EventSystemReset:              true,
	EventSystemImported:           true,
	EventLeaveDataplaneStopFailed: true,
}

// LifecycleEventNames returns the allowlisted api.Event names in a stable
// order so the application can register one typed Wails event per name.
func LifecycleEventNames() []string {
	names := make([]string, 0, len(lifecycleEvent))
	for name := range lifecycleEvent {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var EventPayloads = map[string]any{
	EventConnectionChanged: api.Connection{},
	EventPeersChanged:      []api.Peer{},
	EventRoutesChanged:     []api.Route{},
	EventActivitySample:    api.Activity{},
	EventLogEntry:          api.LogEntry{},
	EventServiceChanged:    api.ServiceStatus{},
	EventUpdateAvailable:   UpdateInfo{},
	EventPeerProbe:         api.ProbeResult{},
	EventVpnPermission:     VpnPermissionEvent{},
}

// watchEvents subscribes to the daemon SSE stream and re-emits typed Wails
// events. It reconnects with a short backoff until ctx is cancelled.
func (s *AppService) watchEvents(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		c, err := ControlClient()
		if err != nil {
			return
		}
		body, err := c.stream(ctx, "/api/v1/events")
		if err != nil {
			sleepCtx(ctx, 1e9)
			continue
		}
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 4096), 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				s.emitEvent(event, strings.TrimPrefix(line, "data: "))
			}
		}
		body.Close()
		if ctx.Err() != nil {
			return
		}
		sleepCtx(ctx, 1e9)
	}
}

func (s *AppService) emitEvent(name, data string) {
	app := application.Get()
	if app == nil || name == "" {
		return
	}
	switch name {
	case EventConnectionChanged:
		var v api.Connection
		if json.Unmarshal([]byte(data), &v) == nil {
			app.Event.Emit(name, v)
		}
	case EventPeersChanged:
		var v []api.Peer
		if json.Unmarshal([]byte(data), &v) == nil {
			app.Event.Emit(name, v)
		}
	case EventRoutesChanged:
		var v []api.Route
		if json.Unmarshal([]byte(data), &v) == nil {
			app.Event.Emit(name, v)
		}
	case EventActivitySample:
		var v api.Activity
		if json.Unmarshal([]byte(data), &v) == nil {
			app.Event.Emit(name, v)
		}
	case EventLogEntry:
		var v api.LogEntry
		if json.Unmarshal([]byte(data), &v) == nil {
			app.Event.Emit(name, v)
		}
	case EventPeerProbe:
		var v api.ProbeResult
		if json.Unmarshal([]byte(data), &v) == nil {
			app.Event.Emit(name, v)
		}
	case EventServiceChanged:
		var v api.ServiceStatus
		if json.Unmarshal([]byte(data), &v) == nil {
			app.Event.Emit(name, v)
		}
	default:
		if !lifecycleEvent[name] {
			return
		}
		var v api.Event
		if json.Unmarshal([]byte(data), &v) == nil {
			app.Event.Emit(name, v)
		}
	}
}

func sleepCtx(ctx context.Context, ns int) {
	select {
	case <-ctx.Done():
	case <-timeAfter(ns):
	}
}
