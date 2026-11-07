// Step 7 (ADR-001 §2.3): daemon system operations. These are the
// normal-mode implementations behind POST /api/v1/system/* — the
// moved helper verbs (rotate-secret, reset, update-daemon) plus the
// ADR-002 leave endpoint and the normal-mode init/join/import
// surface. They are NOT bootstrap operations: bootstrap (Step 4)
// lives in Bootstrap.Handler, serves an unconfigured daemon, and
// is the only path that mints a first config or grants a first
// secret role. A system op on a configured daemon never grants a
// role, never mints a token, and never touches bootstrap state.
//
// Single-writer rule (Step 3): every config change here goes
// through persistLocked → configstore. The only exceptions are
// byte copies (backups) and byte restores (Step 6 RollbackDaemon),
// which never parse or edit config content; Step 9's sweep must
// record the rollback exception, as Step 6 already notes.
//
// The daemon never restarts its own service from inside a request
// handler (plan §2.3, service/restart row): rotate and import
// return RestartRequired=true and the caller restarts via the
// minimal helper; reset returns ServiceActionRequired="stop" and
// the caller stops via the helper. The reason is not just
// deadlock: a handler that kills its own process cannot report
// its own outcome.
package daemon

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/peer"
)

// identityPath derives the identity file beside the config file,
// matching app.Run's convention (dir(config)/identity.key). Empty
// when the daemon has no config path (in-memory/test daemons
// without one), in which case backup/reset skip the identity
// file instead of inventing a path.
func (d *Daemon) identityPath() string {
	cfg := d.config()
	if cfg.Path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.Path), "identity.key")
}

func (d *Daemon) departed() bool {
	return d.config().Node.Departed
}

func requireConfirm(confirm bool, op string) error {
	if !confirm {
		return &api.StatusError{
			Status:      http.StatusBadRequest,
			Code:        api.ErrCodeValidationFailed,
			Message:     op + " requires confirm:true",
			Remediation: "retry with confirm:true",
			Details:     map[string]string{"field": "confirm"},
		}
	}
	return nil
}

// backupSystem copies the current config + identity bytes into a
// timestamped directory next to the config file:
// <config>.rotate-bak-<stamp>/ or <config>.reset-bak-<stamp>/ (and
// <config>.import-bak-<stamp>/ for import). These are byte copies,
// not config writes: nothing is parsed or edited. Missing files
// are skipped; the directory is created only when at least one
// file exists. The caller owns pruning (see pruneBackups).
func (d *Daemon) backupSystem(kind string) (string, error) {
	cfg := d.config()
	if cfg.Path == "" {
		return "", errors.New("daemon: system backup needs a config path")
	}
	var files []string
	if _, err := os.Stat(cfg.Path); err == nil {
		files = append(files, cfg.Path)
	}
	if idPath := d.identityPath(); idPath != "" {
		if _, err := os.Stat(idPath); err == nil {
			files = append(files, idPath)
		}
	}
	if len(files) == 0 {
		return "", nil
	}
	stamp := time.Now().Format("20060102-150405")
	dst := cfg.Path + "." + kind + "-bak-" + stamp
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return "", fmt.Errorf("daemon: backup dir: %w", err)
	}
	// Backups stay private even when set-owner grants an interactive
	// user access to the parent. The Unix mode remains 0700.
	if err := fileperm.RestrictDACL(dst); err != nil {
		return "", fmt.Errorf("daemon: backup dir: %w", err)
	}
	for _, src := range files {
		raw, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("daemon: backup %s: %w", src, err)
		}
		if err := writeBytesAtomic(filepath.Join(dst, filepath.Base(src)), raw); err != nil {
			return "", fmt.Errorf("daemon: backup %s: %w", src, err)
		}
	}
	return dst, nil
}

// writeBytesAtomic writes raw bytes via temp+rename in the target
// directory (O_EXCL temp, fsync semantics via configstore's
// atomicWrite pattern, reimplemented here because configstore
// refuses to write non-config paths). Used for backups and the
// update-state file, both of which live in the system directory.
//
// Explicit DACLs keep replacement backup/update files scoped to their creator,
// SYSTEM, and Administrators. Unix CreateTemp already supplies mode 0600.
func writeBytesAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := fileperm.Restrict(name); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// pruneBackups keeps the newest keep backup directories of one
// kind (e.g. "rotate") next to the config file and removes older
// ones, logging every prune. It never removes the directory named
// by keepPath (the backup just created by the current operation),
// and it never runs on a failed operation — the caller prunes
// only after success, so a failed rotate/reset/update keeps its
// evidence. Backup retention decision (Step 7, owning Step 6's
// open lifecycle): keep 3 per kind, per plan §6.4's last-3 rule,
// applied uniformly to rotate/reset/import backup directories
// AND to Step 6's .upgrade-bak-* config files (same directory,
// same timestamp sort). The .old binary backup is NOT pruned
// here: it is the rollback target for the currently registered
// binary, and deleting it would strand RollbackDaemon; it is
// overwritten by the next upgrade, which is its own lifecycle.
func (d *Daemon) pruneBackups(kind, keepPath string) {
	pruneBackupFiles(d.config().Path, kind, keepPath)
}

// pruneBackupFiles is the free-function form of pruneBackups, so
// the detached apply process (which has no Daemon) can prune
// update backups the same way.
func pruneBackupFiles(configPath, kind, keepPath string) {
	if configPath == "" {
		return
	}
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var dirs []string
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && strings.HasPrefix(name, base+"."+kind+"-bak-") {
			dirs = append(dirs, filepath.Join(dir, name))
		}
		if !e.IsDir() && kind == "upgrade" && strings.HasPrefix(name, base+".upgrade-bak-") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(dirs) // timestamp suffixes sort chronologically
	sort.Strings(files)
	prune := func(list []string) {
		for len(list) > 3 {
			victim := list[0]
			list = list[1:]
			if victim == keepPath {
				continue
			}
			if kind == "upgrade" {
				_ = os.Remove(victim)
			} else {
				_ = os.RemoveAll(victim)
			}
		}
	}
	prune(dirs)
	prune(files)
}

// closeAllSessions drops every live session and direct route.
// It does not remove peers from the table: the peer list is
// config state (persisted by persistLocked), and leave/rotate
// must not silently delete the membership list a rejoin needs.
func (d *Daemon) closeAllSessions() {
	for _, p := range d.peers.List() {
		d.sessions.RemovePeer(p.ID)
		d.routes.RemoveDirect(p.ID)
	}
	if eng := d.engine(); eng != nil {
		for _, p := range d.peers.List() {
			eng.DropSession(p.ID)
		}
	}
}

// RotateSecret implements POST /api/v1/system/rotate-secret.
// It backs up config+identity, generates a fresh network secret,
// persists it through the single writer, closes all sessions, and
// returns the new join string once. It deliberately does NOT
// rekey in-process: the session manager (session.Manager) holds
// its PSK privately with no setter, and inventing a PSK swap
// mid-handshake would risk Noise state corruption. The plan's
// documented fallback therefore applies: the new secret takes
// effect after a service restart (RestartRequired=true), which
// the caller performs via the minimal helper. Until that
// restart, peers still prove the OLD secret — rotate is not
// complete until the restart, and the response says so. The
// backup is pruned to last-3 only after a successful persist.
func (d *Daemon) RotateSecret(ctx context.Context, confirm bool) (api.RotateResult, error) {
	if err := requireConfirm(confirm, "rotate-secret"); err != nil {
		return api.RotateResult{}, err
	}
	d.sysMu.Lock()
	defer d.sysMu.Unlock()
	backup, err := d.backupSystem("rotate")
	if err != nil {
		return api.RotateResult{}, err
	}
	secret := make([]byte, identity.KeySize)
	if _, err := rand.Read(secret); err != nil {
		return api.RotateResult{}, fmt.Errorf("daemon: rotate: random secret: %w", err)
	}
	d.persistMu.Lock()
	cfg := d.config()
	cfg.Node.NetworkSecret = secret
	// Rotation is the ADR-002 eviction default: a rotated node is
	// a new network, so a departed flag from the old network must
	// not carry over and block the new one.
	cfg.Node.Departed = false
	d.cfgMu.Lock()
	d.cfg = cfg
	d.cfgMu.Unlock()
	d.persistLocked(cfg)
	d.persistMu.Unlock()
	d.closeAllSessions()
	d.emit(api.Event{Type: "secret_rotated"})
	if backup != "" {
		d.pruneBackups("rotate", backup)
	}
	inv, err := d.NetworkInvite(ctx)
	if err != nil {
		// The secret is already rotated and persisted; failing the
		// response would hide a completed rotation. Return the
		// backup and restart flag with an empty join and let the
		// caller fetch a fresh invite from /invites/network.
		return api.RotateResult{Backup: backup, RestartRequired: true}, nil
	}
	return api.RotateResult{Join: inv.Value, Backup: backup, RestartRequired: true}, nil
}

// ResetSystem implements POST /api/v1/system/reset. It backs up
// (or, with purge, deletes without backup) the config, removes
// the identity unless kept, removes the firewall rules the node
// added, and returns the backup path. It does NOT stop the
// service or delete its registration: stopping from inside the
// handler would deadlock the response, and registration deletion
// is uninstall (minimal helper), not reset. The caller sequence
// is reset → helper stop → Setup (plan §8). Firewall removal is
// best-effort by the firewall package (missing rules are not an
// error), but a real removal failure is reported in
// FirewallError instead of swallowed — the old helper discarded
// it and left orphaned rules (Agent C).
func (d *Daemon) ResetSystem(ctx context.Context, opts api.ResetOptions) (api.ResetResult, error) {
	if err := requireConfirm(opts.Confirm, "reset"); err != nil {
		return api.ResetResult{}, err
	}
	_ = ctx
	d.sysMu.Lock()
	defer d.sysMu.Unlock()
	cfg := d.config()
	if cfg.Path == "" {
		return api.ResetResult{}, errors.New("daemon: reset needs a config path")
	}
	var backup string
	var err error
	if opts.Purge {
		if err := os.Remove(cfg.Path); err != nil && !os.IsNotExist(err) {
			return api.ResetResult{}, err
		}
		if !opts.KeepIdentity {
			if idPath := d.identityPath(); idPath != "" {
				if err := os.Remove(idPath); err != nil && !os.IsNotExist(err) {
					return api.ResetResult{}, err
				}
			}
		}
	} else {
		backup, err = d.backupSystem("reset")
		if err != nil {
			return api.ResetResult{}, err
		}
		if err := os.Remove(cfg.Path); err != nil && !os.IsNotExist(err) {
			return api.ResetResult{}, err
		}
		if !opts.KeepIdentity {
			if idPath := d.identityPath(); idPath != "" {
				if err := os.Remove(idPath); err != nil && !os.IsNotExist(err) {
					return api.ResetResult{}, err
				}
			}
		}
		if backup != "" {
			d.pruneBackups("reset", backup)
		}
	}
	// Firewall rules last, after config/identity are gone: a
	// firewall failure must not resurrect a half-reset node, and
	// the error is reported, not swallowed.
	fwErr := ""
	if err := firewall.Remove(firewall.Config{
		Interface: cfg.Node.Interface,
		Port:      cfg.Node.ListenPort,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix.String(),
		PrefixV6:  cfg.Node.OverlayIPv6Prefix.String(),
	}); err != nil {
		fwErr = err.Error()
	}
	d.emit(api.Event{Type: "system_reset"})
	res := api.ResetResult{
		Backup:                backup,
		ServiceActionRequired: "stop",
		FirewallError:         fwErr,
	}
	if opts.Purge {
		res.Message = "config and identity removed (purge); stop the service with the minimal helper, then set up again"
	} else {
		res.Message = "config removed; stop the service with the minimal helper, then set up again"
	}
	return res, nil
}

// LeaveSystem implements POST /api/v1/system/leave (ADR-002).
// Order matters: the departed flag is persisted FIRST, so a
// crash after leave cannot silently rejoin; only then is the
// dataplane stopped and sessions/routes closed. A peer leaves
// ITSELF only — there is no target parameter and no other peer
// is touched. M4 P8 gossips the departure as a tombstone before the
// dataplane stops: Propagated is true only when that broadcast
// reached at least one peer. The local tombstone is the persisted
// departed flag plus the peer/route counts in the response; remote
// tombstones are confirmed by self-announcement or two distinct
// origins, retained 180 s, and cleared by the peer's own next
// STATE (see control.handleDeparted).
func (d *Daemon) LeaveSystem(ctx context.Context, confirm bool) (api.LeaveResult, error) {
	if err := requireConfirm(confirm, "leave"); err != nil {
		return api.LeaveResult{}, err
	}
	_ = ctx
	d.sysMu.Lock()
	defer d.sysMu.Unlock()
	if d.departed() {
		return api.LeaveResult{}, &api.StatusError{
			Status:      http.StatusConflict,
			Code:        api.ErrCodeConflict,
			Message:     "node already left; rejoin with a fresh invite or reset",
			Remediation: "call POST /api/v1/system/join with a fresh invite",
		}
	}
	peersBefore := d.peers.List()
	routesBefore := len(d.routes.Routes())
	d.persistMu.Lock()
	cfg := d.config()
	cfg.Node.Departed = true
	d.cfgMu.Lock()
	d.cfg = cfg
	d.cfgMu.Unlock()
	d.persistLocked(cfg)
	d.persistMu.Unlock()
	// M4 P8: gossip the departure BEFORE stopping the dataplane, or
	// there is no transport left to carry it. Propagated is true only
	// when the broadcast actually reached at least one peer; a test
	// daemon with no live engine reports false, honestly.
	propagated := false
	if d.ctl != nil {
		d.ctl.AnnounceDeparted(d.id.ID)
		propagated = d.ctl.BroadcastNow()
	}
	// Stop the dataplane only if it is actually running: a test
	// daemon (never Run) sits in ConnConnecting with no generation
	// channels, and Disconnect would refuse it anyway. In a
	// running daemon, Connected implies a generation exists.
	if d.Connection().State == ConnConnected {
		if _, err := d.Disconnect(context.Background()); err != nil {
			// The flag is already persisted, so leave has
			// happened even if the pause raced; log, do not
			// roll the flag back.
			d.emit(api.Event{Type: "leave_dataplane_stop_failed", Detail: err.Error()})
		}
	}
	d.closeAllSessions()
	routesAfter := len(d.routes.Routes())
	res := api.LeaveResult{
		Departed:        true,
		Propagated:      propagated,
		PeersLeft:       len(peersBefore),
		RoutesWithdrawn: routesBefore - routesAfter,
		LeftAtMS:        time.Now().UnixMilli(),
	}
	if res.RoutesWithdrawn < 0 {
		res.RoutesWithdrawn = 0
	}
	d.emit(api.Event{Type: "peer:leave", Detail: "self"})
	d.emitPeerChanged()
	d.emitRouteChanged()
	return res, nil
}

// SystemInit is the normal-mode init endpoint: it always refuses.
// Bootstrap (Step 4) is the only init path, and it runs on an
// unconfigured daemon. Re-initialising a configured node over
// its live config would destroy membership silently; the 409
// remediation points at reset-then-bootstrap instead.
func (d *Daemon) SystemInit(ctx context.Context) error {
	_ = ctx
	return &api.StatusError{
		Status:      http.StatusConflict,
		Code:        api.ErrCodeConflict,
		Message:     "node is already configured; normal-mode init is refused",
		Remediation: "to start over, call POST /api/v1/system/reset (secret, confirm:true), stop the service with the minimal helper, then bootstrap via the GUI",
	}
}

// SystemJoin is the normal-mode join endpoint, distinct from
// bootstrap join in every way that matters:
//   - Same network (invite secret == current secret): the
//     inviter is added as a peer (AddPeer). This is "join a peer
//     to my network", not "join a network".
//   - Departed + same network + confirm:true: the departed flag
//     is cleared and persisted — the ADR-002 rejoin-by-fresh-
//     invite, enforced, not assumed. Without confirm, 400.
//   - Different network secret: 409. A configured node cannot
//     silently switch networks; reset first, then bootstrap.
//   - Departed + different network: 409 for the same reason.
//
// A peer invite (skein:1:) is not a network invite and is
// rejected here; peer adds go through POST /api/v1/peers.
func (d *Daemon) SystemJoin(ctx context.Context, invite string, confirm bool) (api.JoinResult, error) {
	_ = ctx
	inv, err := identity.DecodeNetworkInvite(strings.TrimSpace(invite))
	if err != nil {
		return api.JoinResult{}, &api.StatusError{
			Status:      http.StatusBadRequest,
			Code:        api.ErrCodeValidationFailed,
			Message:     "invalid network invite: " + err.Error(),
			Remediation: "provide a skein:2: network invite in the body",
			Details:     map[string]string{"field": "invite"},
		}
	}
	d.sysMu.Lock()
	defer d.sysMu.Unlock()
	cfg := d.config()
	if len(cfg.Node.NetworkSecret) == 0 || !equalSecret(inv.Secret, cfg.Node.NetworkSecret) {
		return api.JoinResult{}, &api.StatusError{
			Status:      http.StatusConflict,
			Code:        api.ErrCodeConflict,
			Message:     "invite is for a different network; this node is already a member of another network",
			Remediation: "to switch networks, call POST /api/v1/system/reset (secret, confirm:true), stop the service, then bootstrap with the new invite",
		}
	}
	if d.departed() {
		if !confirm {
			return api.JoinResult{}, &api.StatusError{
				Status:      http.StatusBadRequest,
				Code:        api.ErrCodeValidationFailed,
				Message:     "rejoin after leave requires confirm:true",
				Remediation: "retry with confirm:true",
				Details:     map[string]string{"field": "confirm"},
			}
		}
		d.persistMu.Lock()
		cfg.Node.Departed = false
		d.cfgMu.Lock()
		d.cfg = cfg
		d.cfgMu.Unlock()
		d.persistLocked(cfg)
		d.persistMu.Unlock()
		d.emit(api.Event{Type: "peer:rejoined"})
		return api.JoinResult{Rejoined: true, Message: "departed flag cleared; connect to resume the dataplane"}, nil
	}
	if inv.ID == d.id.ID {
		return api.JoinResult{}, &api.StatusError{
			Status:      http.StatusBadRequest,
			Code:        api.ErrCodeValidationFailed,
			Message:     "invite is for this node",
			Remediation: "provide an invite from another peer",
		}
	}
	// AddPeer decodes a peer (skein:1:) invite, not a network
	// invite, so register the inviter directly, using the same
	// dialer rule (lower ID dials).
	dialer := d.id.ID < inv.ID
	if err := d.registerPeer(inv.ID, inv.PublicKey, inv.Addrs, dialer, ""); err != nil {
		return api.JoinResult{}, err
	}
	for _, addr := range inv.Addrs {
		d.peers.AddCandidate(inv.ID, addr, peer.SourceConfig)
	}
	d.emit(api.Event{Type: "peer_added", PeerID: fmt.Sprintf("%016x", inv.ID)})
	d.persistPeers()
	d.emitPeerChanged()
	return api.JoinResult{PeerID: fmt.Sprintf("%016x", inv.ID), Message: "peer added to the current network"}, nil
}

func equalSecret(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	// Secrets are fixed-size keys; a byte-wise compare is fine
	// (no timing-oracle concern for a value the caller already
	// supplied in full).
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SystemImport replaces the node's config+identity from a source
// file, after the Agent E fix already applied in bootstrap
// import: the source is opened with O_NOFOLLOW, fstat-checked
// (regular file, owner-only mode), and read via the fd — never
// re-opened by path after validation. The current files are
// backed up first; the new files are written through the single
// writer (config) and WriteRaw (identity, under the same lock —
// the documented Step 3 import exception, since replacement is
// the point and the old key is already backed up). The live
// session manager and identity are bound at construction, so
// the new network takes effect only after a service restart
// (RestartRequired=true); sessions are closed now so no traffic
// continues under the old identity in the meantime.
func (d *Daemon) SystemImport(ctx context.Context, source string, confirm bool) (api.ImportResult, error) {
	if err := requireConfirm(confirm, "import"); err != nil {
		return api.ImportResult{}, err
	}
	_ = ctx
	d.sysMu.Lock()
	defer d.sysMu.Unlock()
	cfg, id, err := loadImportSource(source)
	if err != nil {
		return api.ImportResult{}, err
	}
	cfg.Path = d.config().Path
	// Preserve the service control transport: an import must not
	// silently re-enable dev TCP or move the socket/pipe.
	cur := d.config()
	cfg.API = cur.API
	if err := conf.Validate(cfg); err != nil {
		return api.ImportResult{}, err
	}
	backup, err := d.backupSystem("import")
	if err != nil {
		return api.ImportResult{}, err
	}
	store := d.store
	if store == nil {
		if cfg.Path == "" {
			return api.ImportResult{}, errors.New("daemon: import needs a config path")
		}
		var err error
		store, err = configstore.Open(cfg.Path)
		if err != nil {
			return api.ImportResult{}, err
		}
		defer store.Close()
	}
	if idPath := d.identityPath(); idPath != "" {
		if err := store.WriteRaw(idPath, id.PrivateKey); err != nil {
			return api.ImportResult{}, fmt.Errorf("daemon: import identity: %w", err)
		}
	}
	if err := store.Write(cfg); err != nil {
		return api.ImportResult{}, fmt.Errorf("daemon: import config: %w", err)
	}
	d.cfgMu.Lock()
	d.cfg = cfg
	d.cfgMu.Unlock()
	d.closeAllSessions()
	d.emit(api.Event{Type: "system_imported"})
	if backup != "" {
		d.pruneBackups("import", backup)
	}
	return api.ImportResult{Backup: backup, RestartRequired: true}, nil
}

// ReloadGrants re-reads the access file the daemon was
// constructed with, closing the Step 1 open item: grant changes
// used to need a daemon restart. LoadGrantsFromFile leaves the
// table untouched on a malformed file (it returns the error
// before SetGrants), so a bad edit fails closed instead of
// silently clearing every grant; a missing file unconfigures
// the table (kernel callers read-only), matching construction.
func (d *Daemon) ReloadGrants(ctx context.Context) error {
	_ = ctx
	return d.api.LoadGrantsFromFile(controlAccessFile)
}

// SetUpdateStagingDir overrides the update staging directory
// (tests). Production leaves it empty and derives it.
func (d *Daemon) SetUpdateStagingDir(dir string) {
	d.stagingDirOverride = dir
}
