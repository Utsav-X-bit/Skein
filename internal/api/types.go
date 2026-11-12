// Package api implements the authenticated loopback REST and SSE API.
package api

// Status is the node status response.
type Status struct {
	ID            string     `json:"id"`
	Name          string     `json:"name,omitempty"`
	OverlayIPv4   string     `json:"overlay_ipv4"`
	OverlayIPv6   string     `json:"overlay_ipv6,omitempty"`
	UptimeSeconds int64      `json:"uptime_seconds"`
	Version       string     `json:"version"`
	ListenPort    uint16     `json:"listen_port"`
	PeerCount     int        `json:"peers"`
	RxBytes       uint64     `json:"rx_bytes"`
	TxBytes       uint64     `json:"tx_bytes"`
	Connection    Connection `json:"connection"`
	Health        string     `json:"health"`
}

// Peer is one row of the peer table.
type Peer struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	State       string   `json:"state"`
	Path        string   `json:"path,omitempty"`
	RTTMs       float64  `json:"rtt_ms"`
	OverlayIPv4 string   `json:"overlay_ipv4,omitempty"`
	OverlayIPv6 string   `json:"overlay_ipv6,omitempty"`
	Endpoint    string   `json:"endpoint,omitempty"`
	Transport   string   `json:"transport,omitempty"`
	TransportRx string   `json:"transport_rx,omitempty"`
	Candidates  []string `json:"candidates,omitempty"`
	Dialer      bool     `json:"dialer"`
	Direct      bool     `json:"direct"`
	RxBytes     uint64   `json:"rx_bytes"`
	TxBytes     uint64   `json:"tx_bytes"`
	LastSeenMS  int64    `json:"last_seen_ms"`
	// SessionStartMS and LastHandshakeMS are epoch milliseconds, not ages.
	// They are omitted when no session has been observed, so the UI shows
	// "not observed" instead of 1970.
	SessionStartMS  *int64 `json:"session_start_ms,omitempty"`
	LastHandshakeMS *int64 `json:"last_handshake_ms,omitempty"`
}

// Route is one resolved route.
type Route struct {
	Destination string   `json:"destination"`
	NextHop     string   `json:"next_hop"`
	CostMs      float64  `json:"cost_ms"`
	Hops        uint8    `json:"hops"`
	Path        []string `json:"path,omitempty"`
}

// Link is one peer-to-peer edge a neighbour advertised: From reaches
// Destination in Hops links through Path, which starts at From.
type Link struct {
	From        string   `json:"from"`
	Destination string   `json:"destination"`
	Hops        uint8    `json:"hops"`
	CostMs      float64  `json:"cost_ms"`
	Path        []string `json:"path,omitempty"`
}

// Activity is a payload-free, aggregate authenticated mesh link sample.
type Activity struct {
	Origin      string   `json:"origin"`
	Destination string   `json:"destination"`
	Seq         uint64   `json:"seq"`
	RxBytes     uint64   `json:"rx_bytes"`
	TxBytes     uint64   `json:"tx_bytes"`
	RTTMs       float64  `json:"rtt_ms"`
	JitterMs    float64  `json:"jitter_ms"`
	Transport   string   `json:"transport"`
	Direct      bool     `json:"direct"`
	ObservedMS  int64    `json:"observed_ms"`
	ExpiresMS   int64    `json:"expires_ms"`
	Path        []string `json:"path,omitempty"`
}

// ConfigView is the redacted effective configuration.
type ConfigView struct {
	Name              string `json:"name,omitempty"`
	OverlayIPv4Prefix string `json:"overlay_ipv4_prefix"`
	OverlayIPv6Prefix string `json:"overlay_ipv6_prefix"`
	OverlayIPv4       string `json:"overlay_ipv4"`
	OverlayIPv6       string `json:"overlay_ipv6,omitempty"`
	ListenPort        uint16 `json:"listen_port"`
	MTU               int    `json:"mtu"`
	// EffectiveMTU is the live send limit after path-MTU negotiation; zero
	// while the dataplane is down.
	EffectiveMTU int      `json:"effective_mtu,omitempty"`
	Interface    string   `json:"interface"`
	APIListen    string   `json:"api_listen"`
	APISocket    string   `json:"api_socket,omitempty"`
	APIPipe      string   `json:"api_pipe,omitempty"`
	MDNS         bool     `json:"mdns"`
	STUN         bool     `json:"stun"`
	STUNServer   string   `json:"stun_server"`
	PublicAddr   string   `json:"public_addr,omitempty"`
	ConfigPath   string   `json:"config_path,omitempty"`
	Protocol     int      `json:"protocol"`
	Peers        []string `json:"peers"`
	// Departed is the ADR-002 leave flag (Step 7): true means this
	// node left its network and will not rejoin until a fresh
	// invite clears it.
	Departed bool `json:"departed,omitempty"`
	// Secrets are always reported as redacted.
	NetworkSecret string `json:"network_secret"`
	APIToken      string `json:"api_token"`
}

// Event is one SSE event.
type Event struct {
	ID     uint64 `json:"id,omitempty"`
	Type   string `json:"type"`
	PeerID string `json:"peer_id,omitempty"`
	Detail string `json:"detail,omitempty"`
	TimeMS int64  `json:"time_ms"`
}

// SetNameRequest changes the node's advertised name.
type SetNameRequest struct {
	Name string `json:"name"`
}

// AddPeerRequest accepts either an invite string or an explicit peer.
type AddPeerRequest struct {
	Invite string `json:"invite,omitempty"`
}
