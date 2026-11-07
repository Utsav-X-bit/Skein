package daemon

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
)

func findPeerRow(t *testing.T, rows []api.Peer, id uint64) api.Peer {
	t.Helper()
	want := fmt.Sprintf("%016x", id)
	for _, row := range rows {
		if row.ID == want {
			return row
		}
	}
	t.Fatalf("peer %s missing from %d rows", want, len(rows))
	return api.Peer{}
}

func TestPeersProjectSessionTimestamps(t *testing.T) {
	d, _ := newSystemDaemon(t)
	id := addTestPeer(t, d)
	start := time.UnixMilli(1_700_000_000_000)
	handshake := start.Add(30 * time.Second)
	d.peers.MarkSession(id, start)
	d.peers.SetActive(id, netip.MustParseAddrPort("192.0.2.9:11010"), time.Millisecond, "udp", start)
	d.peers.MarkSession(id, handshake)

	row := findPeerRow(t, d.Peers(), id)
	if row.SessionStartMS == nil || *row.SessionStartMS != start.UnixMilli() {
		t.Fatalf("session_start_ms = %v, want %d", row.SessionStartMS, start.UnixMilli())
	}
	if row.LastHandshakeMS == nil || *row.LastHandshakeMS != handshake.UnixMilli() {
		t.Fatalf("last_handshake_ms = %v, want %d", row.LastHandshakeMS, handshake.UnixMilli())
	}
}

func TestPeersOmitUnobservedSessionTimestamps(t *testing.T) {
	d, _ := newSystemDaemon(t)
	id := addTestPeer(t, d)
	row := findPeerRow(t, d.Peers(), id)
	if row.SessionStartMS != nil || row.LastHandshakeMS != nil {
		t.Fatalf("unobserved timestamps = %v/%v, want nil", row.SessionStartMS, row.LastHandshakeMS)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"session_start_ms", "last_handshake_ms"} {
		if strings.Contains(string(raw), field) {
			t.Fatalf("unknown %s leaked into JSON: %s", field, raw)
		}
	}
}
