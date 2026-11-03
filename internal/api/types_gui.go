package api

// Capabilities describes what this daemon supports. The desktop app refuses a
// protocol mismatch and hides features whose flag is false.
type Capabilities struct {
	AppVersion string          `json:"app_version"`
	Protocol   int             `json:"protocol"`
	Platform   string          `json:"platform"`
	Features   map[string]bool `json:"features"`
}

// Identity is the local node's public identity. It never contains the private
// key or network secret.
type Identity struct {
	ID        string `json:"id"`
	PublicKey string `json:"public_key"`
	Name      string `json:"name,omitempty"`
}

// Connection is the dataplane state, separate from service state.
type Connection struct {
	State        string `json:"state"`
	PausedReason string `json:"paused_reason,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	SinceMS      int64  `json:"since_ms"`
}

// ServiceStatus is read without elevation from SCM or systemd.
type ServiceStatus struct {
	State     string `json:"state"`
	Manager   string `json:"manager"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// ConfigPatch is a partial update. Only the allowlisted fields are applied.
type ConfigPatch struct {
	Name       *string `json:"name,omitempty"`
	ListenPort *uint16 `json:"listen_port,omitempty"`
	MTU        *int    `json:"mtu,omitempty"`
	Interface  *string `json:"interface,omitempty"`
	PublicAddr *string `json:"public_addr,omitempty"`
	MDNS       *bool   `json:"mdns,omitempty"`
	STUN       *bool   `json:"stun,omitempty"`
	STUNServer *string `json:"stun_server,omitempty"`
}

// ConfigPatchResult reports what was applied and whether a restart is needed.
type ConfigPatchResult struct {
	RestartRequired bool     `json:"restart_required"`
	Applied         []string `json:"applied"`
}

// Invite is a peer invite (no secret) or network join (secret-bearing).
type Invite struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Warning  string `json:"warning,omitempty"`
	Snapshot bool   `json:"snapshot,omitempty"`
}

// LogEntry is one daemon log line.
type LogEntry struct {
	TimeMS  int64  `json:"time_ms"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// FirewallRule is one rule Skein manages.
type FirewallRule struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`
	Action    string `json:"action"`
	Protocol  string `json:"protocol"`
	Port      string `json:"port,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// FirewallStatus reports the manager and the rules Skein believes it owns.
type FirewallStatus struct {
	Manager     string         `json:"manager"`
	State       string         `json:"state"`
	Rules       []FirewallRule `json:"rules"`
	LastCheckMS int64          `json:"last_check_ms"`
	LastError   string         `json:"last_error,omitempty"`
}

// SecurityStatus exposes permission/ownership facts without secrets.
type SecurityStatus struct {
	ConfigPath      string   `json:"config_path"`
	ConfigOwner     string   `json:"config_owner"`
	ConfigMode      string   `json:"config_mode"`
	IdentityOwner   string   `json:"identity_owner"`
	IdentityMode    string   `json:"identity_mode"`
	SocketPath      string   `json:"socket_path,omitempty"`
	AllowedUIDs     []uint32 `json:"allowed_uids,omitempty"`
	UpdateSignature string   `json:"update_signature"`
	FirewallManager string   `json:"firewall_manager"`
}

// DiagnosticBundle is the redacted support export.
type DiagnosticBundle struct {
	GeneratedMS int64      `json:"generated_ms"`
	Version     string     `json:"version"`
	Protocol    int        `json:"protocol"`
	Platform    string     `json:"platform"`
	Logs        []LogEntry `json:"logs"`
	Peers       []Peer     `json:"peers"`
	Routes      []Route    `json:"routes"`
	Config      ConfigView `json:"config"`
	Redactions  []string   `json:"redactions"`
}

// ProbeResult is emitted as a peer:probe event.
type ProbeResult struct {
	PeerID string  `json:"peer_id"`
	State  string  `json:"state"`
	RTTMs  float64 `json:"rtt_ms,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// APIError is the structured error envelope.
type APIError struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	Remediation string            `json:"remediation,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
	RequestID   string            `json:"request_id,omitempty"`
}
