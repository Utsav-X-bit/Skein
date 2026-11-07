package services

import (
	"strings"
	"testing"
)

// The daemon reports peer-down, departure and dialer fallback as api.Event
// envelopes (internal/control/control.go). They must stay on the forwarding
// allowlist, or the GUI session feed silently loses them.
func TestLifecycleEventNamesForwardsPeerTransitions(t *testing.T) {
	names := LifecycleEventNames()
	joined := strings.Join(names, ",")
	for _, want := range []string{EventPeerDown, EventPeerDeparted, EventDialerFallback} {
		if !strings.Contains(","+joined+",", ","+want+",") {
			t.Errorf("LifecycleEventNames() missing %q: %v", want, names)
		}
	}
	if lifecycleEvent["not_a_real_event"] {
		t.Fatal("unknown events must stay off the forwarding allowlist")
	}
}
