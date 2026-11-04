package api

import "context"

// Step 7 (ADR-001 §2.3): daemon system endpoints. These are the
// normal-mode counterparts of the bootstrap endpoints and the moved
// helper verbs (rotate-secret, reset, update-daemon). They are NOT
// bootstrap endpoints: they run on a configured daemon, they are
// gated by Step 1 roles via wrapRole, and they never bypass the
// bootstrap grant logic — bootstrap grants are made only by the
// bootstrap flow (Step 4), never by these endpoints.
//
// Roles (plan §2.3):
//   - rotate-secret, reset, update check/apply, grants reload: secret.
//     Rotate discloses a new network secret; reset destroys
//     identity/config; update apply swaps the service binary; grant
//     reload changes who may call secret endpoints.
//   - leave, system join, system import: mutate, except the
//     departed-clear path of system join, which requires confirm
//     (enforced by the daemon, not the role gate).
//   - system init: there is no successful normal-mode init. The
//     endpoint exists so a configured daemon answers 409 with a
//     remediation ("bootstrap is the init path") instead of a 404
//     that invites a second init implementation. A live node is
//     never re-initialised over its existing config.
//
// No result type here carries a secret except RotateResult.Join,
// which is returned once, in the response body only, and must never
// be logged (the audit hook logs secret_endpoint=true, never the
// body).

// SystemOperator is the daemon-side contract for the system
// endpoints. The API server finds it through a type assertion on
// the state reader, exactly like GUI, so the server core needs no
// extra fields and tests can substitute a fake.
type SystemOperator interface {
	RotateSecret(ctx context.Context, confirm bool) (RotateResult, error)
	ResetSystem(ctx context.Context, opts ResetOptions) (ResetResult, error)
	LeaveSystem(ctx context.Context, confirm bool) (LeaveResult, error)
	SystemInit(ctx context.Context) error
	SystemJoin(ctx context.Context, invite string, confirm bool) (JoinResult, error)
	SystemImport(ctx context.Context, source string, confirm bool) (ImportResult, error)
	UpdateCheck(ctx context.Context, manifestPath string) (UpdateCheckResult, error)
	UpdateApply(ctx context.Context, manifestPath, stagedBinaryPath string, confirm bool) (UpdateApplyResult, error)
	ReloadGrants(ctx context.Context) error
}

// RotateResult is returned once by POST /system/rotate-secret.
// Join carries the new network secret; RestartRequired is true
// because the live session manager holds the old secret (see
// daemon.RotateSecret): the new secret takes effect only after a
// service restart, which the caller performs via the minimal
// helper (restart verb), never from inside the daemon.
type RotateResult struct {
	Join            string `json:"join"`
	Backup          string `json:"backup,omitempty"`
	RestartRequired bool   `json:"restart_required"`
}

// ResetOptions mirrors POST /system/reset.
type ResetOptions struct {
	Purge        bool `json:"purge,omitempty"`
	KeepIdentity bool `json:"keep_identity,omitempty"`
	Confirm      bool `json:"confirm"`
}

// ResetResult reports what reset did. The daemon itself never stops
// its own service from inside the handler (that would deadlock the
// response); ServiceActionRequired always names the helper stop
// the caller must perform next (plan §8 sequence), and the config
// is already gone when the response is sent, so stopping afterwards
// cannot resurrect it. FirewallError is non-empty when rule
// removal failed: reset still succeeds (config/identity are
// already backed up/removed) but the caller must know rules may
// remain — a silent success here was the Agent C orphan bug.
type ResetResult struct {
	Backup                string `json:"backup,omitempty"`
	Message               string `json:"message,omitempty"`
	FirewallError         string `json:"firewall_error,omitempty"`
	ServiceActionRequired string `json:"service_action_required,omitempty"`
}

// LeaveResult reports a local leave (ADR-002). Propagated is true only
// when the version 5 tombstone announcement reached at least one peer
// (M4 gossip); false means no peer was reached, and offline peers learn
// the departure from the retained tombstone on reconnect. A peer
// that leaves is NOT banned and no other peer is touched — there
// is deliberately no "leave this other peer" operation.
type LeaveResult struct {
	Departed        bool  `json:"departed"`
	Propagated      bool  `json:"propagated"`
	PeersLeft       int   `json:"peers_left"`
	RoutesWithdrawn int   `json:"routes_withdrawn"`
	LeftAtMS        int64 `json:"left_at_ms"`
}

// JoinResult reports a normal-mode system join. Rejoined is true
// only on the departed-clear path (a fresh invite for the same
// network after a leave); otherwise the invite added a peer to the
// current network.
type JoinResult struct {
	Rejoined bool   `json:"rejoined"`
	PeerID   string `json:"peer_id,omitempty"`
	Message  string `json:"message,omitempty"`
}

// ImportResult reports a normal-mode system import. As with
// rotate, the new config/identity take effect only after a
// service restart (the live session manager and identity are
// bound at construction), so RestartRequired is always true.
type ImportResult struct {
	Backup          string `json:"backup,omitempty"`
	RestartRequired bool   `json:"restart_required"`
}

// UpdateState is the daemon's last-known update outcome, reported
// by GET /system/update/check and written by the detached apply
// process (see daemon.ApplyUpdateDetached). Backup paths are
// local filesystem paths, not secrets, and are included so an
// operator can recover by hand after a failed apply.
type UpdateState struct {
	Version    string `json:"version,omitempty"`
	Applied    bool   `json:"applied"`
	RolledBack bool   `json:"rolled_back"`
	Error      string `json:"error,omitempty"`
	Backup     string `json:"backup,omitempty"`
	AtMS       int64  `json:"at_ms"`
}

// UpdateCheckResult is GET /system/update/check. Downgrades are
// not returned as update_available=false: they are rejected with
// 409 by the daemon (plan §6.4), so a 200 here always means a
// newer, verified manifest.
type UpdateCheckResult struct {
	Version         string       `json:"version"`
	CurrentVersion  string       `json:"current_version"`
	Checked         bool         `json:"checked"`
	UpdateAvailable bool         `json:"update_available"`
	LastUpdate      *UpdateState `json:"last_update,omitempty"`
}

// UpdateApplyResult is POST /system/update/apply. Apply is
// asynchronous by necessity: the daemon cannot swap its own
// service binary and reply from the same process. Applied=false
// with Detached=true means the detached apply process was
// launched and verified the manifest; its outcome (applied or
// rolled back) appears in UpdateCheck's LastUpdate once it
// finishes. The caller must poll check, not assume success.
type UpdateApplyResult struct {
	Version  string `json:"version"`
	Checked  bool   `json:"checked"`
	Detached bool   `json:"detached"`
	Staged   string `json:"staged,omitempty"`
}
