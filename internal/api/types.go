// Package api implements the authenticated loopback REST and SSE API.
package api

// Status is the node status response.
type Status struct {
	ID            string `json:"id"`
	OverlayIPv4   string `json:"overlay_ipv4"`
	OverlayIPv6   string `json:"overlay_ipv6,omitempty"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Version       string `json:"version"`
	ListenPort    uint16 `json:"listen_port"`
	PeerCount     int    `json:"peers"`
	RxBytes       uint64 `json:"rx_bytes"`
	TxBytes       uint64 `json:"tx_bytes"`
}

// Peer is one row of the peer table.
type Peer struct {
	ID          string   `json:"id"`
	State       string   `json:"state"`
	Path        string   `json:"path,omitempty"`
	RTTMs       float64  `json:"rtt_ms"`
	OverlayIPv4 string   `json:"overlay_ipv4,omitempty"`
	OverlayIPv6 string   `json:"overlay_ipv6,omitempty"`
	Endpoint    string   `json:"endpoint,omitempty"`
	Candidates  []string `json:"candidates,omitempty"`
	Dialer      bool     `json:"dialer"`
	Direct      bool     `json:"direct"`
	RxBytes     uint64   `json:"rx_bytes"`
	TxBytes     uint64   `json:"tx_bytes"`
	LastSeenMS  int64    `json:"last_seen_ms"`
}

// Route is one resolved route.
type Route struct {
	Destination string  `json:"destination"`
	NextHop     string  `json:"next_hop"`
	CostMs      float64 `json:"cost_ms"`
	Hops        uint8   `json:"hops"`
}

// ConfigView is the redacted effective configuration.
type ConfigView struct {
	OverlayIPv4Prefix string   `json:"overlay_ipv4_prefix"`
	OverlayIPv6Prefix string   `json:"overlay_ipv6_prefix"`
	OverlayIPv4       string   `json:"overlay_ipv4"`
	OverlayIPv6       string   `json:"overlay_ipv6,omitempty"`
	ListenPort        uint16   `json:"listen_port"`
	MTU               int      `json:"mtu"`
	Interface         string   `json:"interface"`
	APIListen         string   `json:"api_listen"`
	MDNS              bool     `json:"mdns"`
	Peers             []string `json:"peers"`
	// Secrets are always reported as redacted.
	NetworkSecret string `json:"network_secret"`
	APIToken      string `json:"api_token"`
}

// Event is one SSE event.
type Event struct {
	Type   string `json:"type"`
	PeerID string `json:"peer_id,omitempty"`
	Detail string `json:"detail,omitempty"`
	TimeMS int64  `json:"time_ms"`
}

// AddPeerRequest accepts either an invite string or an explicit peer.
type AddPeerRequest struct {
	Invite string `json:"invite,omitempty"`
}
