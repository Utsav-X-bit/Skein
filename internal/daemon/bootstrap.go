// Bootstrap mode (ADR-001 Step 4) is the daemon's unconfigured state:
// it serves ONLY the control socket/pipe, ONLY the bootstrap endpoints
// plus a bootstrap status, and performs NO mesh activity — no TUN
// device, no UDP port, no mDNS, no firewall rules, no gossip. It is
// not a mesh member. This file contains no dataplane, link, session,
// or discovery calls at all, by construction; grep for them if in doubt.
//
// Flow: the minimal helper mints a single-use token (internal/bootstrap)
// and starts the service; app.Run detects "no config + valid bootstrap
// state" and runs Bootstrap.Serve; the GUI calls exactly one bootstrap
// endpoint with the token in the JSON body; on success the daemon
// writes config + identity as the single writer (configstore, under
// the lifetime lock it already holds), grants the bootstrap caller
// the secret role, deletes the state file, and returns the outcome to
// app.Run, which transitions in-process to a normal Daemon (same
// process, no service restart). An identity.key preserved by
// `system reset --keep-identity` is reused for init/join/import rather
// than regenerated, so the node keeps its peer ID and public key. If
// the transition itself fails, the process exits and the service
// manager restarts it into normal mode, because a config now exists.
//
// Token verification order and burn semantics live in
// internal/bootstrap.Manager.Verify and are not re-implemented here.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/bootstrap"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/version"
	"github.com/Utsav-X-bit/skein/internal/wan"
)

// BootstrapOptions configures bootstrap mode. Socket (Unix) or Pipe
// (Windows) is the control transport the bootstrap listener binds;
// Serve requires one of them, while Handler works without either, so
// tests can exercise the handler over an in-process transport.
type BootstrapOptions struct {
	ConfigPath   string
	IdentityPath string
	StatePath    string
	AccessPath   string
	Socket       string
	Pipe         string
}

// BootstrapResult is the once-only response to a successful bootstrap.
// Join is secret-bearing (it carries the network secret) and is
// returned exactly once, in the response body only — never logged,
// never written to a file by this package.
type BootstrapResult struct {
	NodeID     string `json:"node_id"`
	PublicKey  string `json:"public_key"`
	Name       string `json:"name"`
	Overlay    string `json:"overlay_ipv4"`
	Invite     string `json:"invite"`
	Join       string `json:"join"`
	ConfigPath string `json:"config_path"`
}

// BootstrapOutcome is what Serve returns to app.Run for the
// in-process transition to a normal daemon.
type BootstrapOutcome struct {
	Config   conf.Config
	ID       identity.Identity
	CallerID string
}

// Bootstrap is a daemon in bootstrap mode.
type Bootstrap struct {
	opts   BootstrapOptions
	store  *configstore.Store
	mgr    *bootstrap.Manager
	mu     sync.Mutex // serialises bootstrap attempts
	done   bool
	result BootstrapResult
	cfg    conf.Config
	id     identity.Identity
	caller string

	// identityKept records that build reused an identity.key already on
	// disk (preserved by reset --keep-identity). commit then skips the
	// write, and a failed attempt must not delete the preserved key.
	identityKept bool

	// success is closed once a bootstrap commits. Serve waits on it
	// instead of polling Outcome.
	success chan struct{}

	// closed is closed by Close, so Serve stops even when the caller
	// closes bootstrap without cancelling its context.
	closed    chan struct{}
	closeOnce sync.Once

	// handoff is the control listener Serve bound. It survives the
	// transition so the normal daemon can adopt the same transport
	// rather than closing and rebinding it (B16: clients that
	// connected right after success saw a dead socket/pipe).
	handoff      *ControlHandoff
	handoffTaken bool
}

// NewBootstrap opens the config store (lifetime lock held from here,
// before any state is read, exactly as Step 3 requires) and validates
// the bootstrap state. A config that already exists is a constructor
// error: bootstrap mode never overwrites a configured node. A stale
// state file (expired or already used) is deleted and reported as no
// valid state, because a used/expired token can never bootstrap again
// and must not block a fresh elevation from minting a new token.
func NewBootstrap(opts BootstrapOptions) (*Bootstrap, error) {
	if opts.ConfigPath == "" || opts.IdentityPath == "" || opts.StatePath == "" {
		return nil, errors.New("daemon: bootstrap needs a config path, an identity path, and a state path")
	}
	if _, err := os.Stat(opts.ConfigPath); err == nil {
		return nil, errors.New("daemon: already configured; bootstrap is closed")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("daemon: bootstrap config check: %w", err)
	}
	store, err := configstore.Open(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("daemon: bootstrap config store: %w", err)
	}
	if err := store.Lock(); err != nil {
		store.Close()
		return nil, fmt.Errorf("daemon: %w", err)
	}
	mgr := bootstrap.NewManager(opts.StatePath)
	st, err := mgr.Load()
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("daemon: %w", err)
	}
	if !st.Valid() {
		_ = mgr.Delete()
		store.Close()
		if st.Used {
			return nil, bootstrap.ErrUsed
		}
		return nil, bootstrap.ErrExpired
	}
	return &Bootstrap{
		opts: opts, store: store, mgr: mgr,
		success: make(chan struct{}), closed: make(chan struct{}),
	}, nil
}

// Close releases the config lock. After a successful bootstrap the
// caller (app.Run) closes bootstrap before constructing the normal
// daemon, so the new daemon can re-acquire the lock. If the control
// handoff was not taken by a normal daemon, Close also stops it: a
// bootstrap that ends without a transition must not leave a listener
// running.
func (b *Bootstrap) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	b.mu.Lock()
	h, taken := b.handoff, b.handoffTaken
	b.mu.Unlock()
	if h != nil && !taken {
		h.Stop()
	}
	if b.store != nil {
		return b.store.Close()
	}
	return nil
}

// TakeHandoff transfers the control listener bound by Serve to the
// caller, for the normal daemon to adopt. It returns nil when Serve
// never bound one (handler-only use) or when it was already taken. The
// caller owns the handoff: Close no longer stops it, and the normal
// daemon's shutdown does.
func (b *Bootstrap) TakeHandoff() *ControlHandoff {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handoff == nil || b.handoffTaken {
		return nil
	}
	b.handoffTaken = true
	return b.handoff
}

// GrantSecret applies a secret grant to a live daemon's authorisation
// table directly (Step 1's SetGrant), in addition to the persisted
// access file the daemon loaded at construction. It exists so a grant
// is never "written but not yet effective": app.RunBootstrap calls it
// on the post-transition daemon with the bootstrap caller.
func (d *Daemon) GrantSecret(callerID string) {
	d.api.SetGrant(callerID, api.RoleSecret)
}

// Handler returns the bootstrap-only control API. Every path except
// GET /api/v1/status and the three bootstrap endpoints returns 410
// Gone: in bootstrap mode there is no status/peers/routes/config to
// read and no peer to mutate, and a client that mistakes bootstrap
// mode for a running node must be told so, not served stale data.
func (b *Bootstrap) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if code, ok := b.bootstrapCaller(r); !ok {
			http.Error(w, "bootstrap: kernel control transport required", code)
			return
		}
		// Re-check after the outer gate: a request that passed the gate
		// just before the bootstrap committed must not report bootstrap
		// state after success.
		if b.transitioning(w, r) {
			return
		}
		writeBootstrapJSON(w, http.StatusOK, map[string]any{
			"state":      "bootstrap",
			"configured": false,
			"version":    version.Info(),
		})
	})
	mux.HandleFunc("POST /api/v1/bootstrap/init", func(w http.ResponseWriter, r *http.Request) {
		b.serveBootstrap(w, r, bootstrap.VerbInit)
	})
	mux.HandleFunc("POST /api/v1/bootstrap/join", func(w http.ResponseWriter, r *http.Request) {
		b.serveBootstrap(w, r, bootstrap.VerbJoin)
	})
	mux.HandleFunc("POST /api/v1/bootstrap/import", func(w http.ResponseWriter, r *http.Request) {
		b.serveBootstrap(w, r, bootstrap.VerbImport)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bootstrap mode: only /api/v1/status and /api/v1/bootstrap/* are available", http.StatusGone)
	})
	// A request that arrives after the bootstrap committed belongs to
	// the normal daemon, not to bootstrap mode. Waiting for the adopted
	// handler here (instead of answering "bootstrap" or closing the
	// listener) is what makes the in-process transition invisible to
	// clients: the connection stays open and the response is the real
	// one.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b.transitioning(w, r) {
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// transitioning reports whether this request must be deferred to the
// normal handler because the bootstrap already succeeded. It returns
// true when it handled the response (normal handler, a bounded wait,
// or an explicit 503), false while bootstrap mode still owns it. A
// Bootstrap without a bound listener (handler-only use, tests) keeps
// answering from the bootstrap mux, including its "already completed"
// response.
func (b *Bootstrap) transitioning(w http.ResponseWriter, r *http.Request) bool {
	b.mu.Lock()
	done, h := b.done, b.handoff
	b.mu.Unlock()
	if !done || h == nil {
		return false
	}
	h.awaitReady(w, r)
	return true
}

// bootstrapCaller enforces the transport gate before any token work:
// only a kernel-authenticated caller (socket uid / pipe SID) may even
// attempt a bootstrap. Bearer (dev-TCP) callers get 403 even with a
// valid token, and unknown callers get 401. The returned bool reports
// whether the caller passed the gate.
func (b *Bootstrap) bootstrapCaller(r *http.Request) (int, bool) {
	caller := api.CallerFrom(r.Context())
	if caller.Transport == api.TransportBearer {
		return http.StatusForbidden, false
	}
	if !caller.Known() {
		return http.StatusUnauthorized, false
	}
	return 0, true
}

func writeBootstrapJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// bootstrapRequest is the JSON body of a bootstrap endpoint. Token is
// the base64url plaintext token, in the body only — never in a URL,
// never in argv. The remaining fields mirror app.InitOptions (init),
// the network invite (join), or a source path (import).
type bootstrapRequest struct {
	Token      string `json:"token"`
	Name       string `json:"name"`
	PrefixV4   string `json:"prefix_v4"`
	PrefixV6   string `json:"prefix_v6"`
	Port       uint16 `json:"port"`
	MTU        int    `json:"mtu"`
	MDNS       *bool  `json:"mdns"`
	STUN       *bool  `json:"stun"`
	STUNServer string `json:"stun_server"`
	Invite     string `json:"invite"`
	Source     string `json:"source"`
}

func (b *Bootstrap) serveBootstrap(w http.ResponseWriter, r *http.Request, verb string) {
	if code, ok := b.bootstrapCaller(r); !ok {
		http.Error(w, "bootstrap: kernel control transport required", code)
		return
	}
	caller := api.CallerFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req bootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bootstrap: invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Token == "" {
		http.Error(w, "bootstrap: token is required", http.StatusBadRequest)
		return
	}
	// A malformed token encoding is a client error, not a verification
	// failure: it must not burn the real token's attempt budget.
	token, err := base64.RawURLEncoding.DecodeString(req.Token)
	if err != nil || len(token) != bootstrap.TokenSize {
		http.Error(w, "bootstrap: token is not a valid bootstrap token", http.StatusBadRequest)
		return
	}
	defer clear(token)
	res, status, err := b.bootstrap(r.Context(), verb, caller.ID(), token, req)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	// The result carries the join string once. It is written to the
	// response body only; nothing in this handler logs it.
	writeBootstrapJSON(w, http.StatusOK, res)
}

// bootstrap performs one bootstrap attempt. Field validation and
// invite/source decoding happen BEFORE the token is marked used, so
// a typo (bad port, bad invite) does not consume the token and the
// user can retry without re-elevating. The token is marked used
// atomically before any config write, so a crash after that point
// cannot replay the same token. Any failure after marking used
// removes the partial config/identity files this attempt created
// (a preserved identity is never removed), leaving the node
// unconfigured but the token spent — a fresh elevation is then
// required, which is the safe failure mode, not a silent retry.
func (b *Bootstrap) bootstrap(ctx context.Context, verb, callerID string, token []byte, req bootstrapRequest) (BootstrapResult, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return BootstrapResult{}, http.StatusGone, errors.New("bootstrap: bootstrap already completed")
	}
	if err := b.mgr.Verify(callerID, verb, token); err != nil {
		switch {
		case errors.Is(err, bootstrap.ErrBurned), errors.Is(err, bootstrap.ErrExpired),
			errors.Is(err, bootstrap.ErrUsed), errors.Is(err, bootstrap.ErrNoState):
			return BootstrapResult{}, http.StatusGone, err
		default:
			return BootstrapResult{}, http.StatusForbidden, errors.New("bootstrap: token verification failed")
		}
	}
	// The secret grant follows the token's bound caller, not the
	// socket peer. Under `sudo skein system init` the peer is uid 0
	// while the token is bound to the invoking user; granting the peer
	// would leave root holding secret and the human holding nothing
	// (M9 §2b: the test network's access file read `0:secret` and the
	// user could not revoke). The bound caller is also what the
	// in-process transition grants via the outcome below, so file and
	// live table agree.
	grantID, err := b.mgr.Caller()
	if err != nil {
		return BootstrapResult{}, http.StatusInternalServerError, err
	}
	callerID = grantID
	cfg, id, err := b.build(ctx, verb, req)
	if err != nil {
		return BootstrapResult{}, http.StatusBadRequest, err
	}
	if err := b.mgr.MarkUsed(); err != nil {
		return BootstrapResult{}, http.StatusGone, err
	}
	// From here on, any failure cleans up partial files.
	if err := b.commit(cfg, id, callerID); err != nil {
		b.rollback()
		return BootstrapResult{}, http.StatusInternalServerError, err
	}
	res, err := b.resultFor(ctx, cfg, id)
	if err != nil {
		b.rollback()
		return BootstrapResult{}, http.StatusInternalServerError, err
	}
	b.done = true
	b.cfg = cfg
	b.id = id
	b.caller = callerID
	b.result = res
	// Every request that arrives from this instant is deferred to the
	// normal handler by transitioning; the listener itself stays open.
	if b.handoff != nil {
		b.handoff.BeginTransition()
	}
	close(b.success)
	return res, http.StatusOK, nil
}

// commit writes identity + config through the single writer, applies
// root ownership (matching the helper's writeRoot invariant), grants
// the token's bound caller the secret role in the access file, and
// deletes the state file — in that order, so a grant is never
// recorded for a bootstrap that did not persist, and a spent token
// never lingers after a successful bootstrap. A preserved identity
// (reset --keep-identity) is already on disk and is not rewritten:
// overwriting it would change the node's peer ID, and configstore's
// WriteIdentity correctly refuses to overwrite.
func (b *Bootstrap) commit(cfg conf.Config, id identity.Identity, callerID string) error {
	if !b.identityKept {
		if err := b.store.WriteIdentity(b.opts.IdentityPath, id); err != nil {
			return fmt.Errorf("bootstrap: write identity: %w", err)
		}
		if err := fileperm.Root(b.opts.IdentityPath); err != nil {
			return fmt.Errorf("bootstrap: own identity: %w", err)
		}
	}
	if err := b.store.Write(cfg); err != nil {
		return fmt.Errorf("bootstrap: write config: %w", err)
	}
	if err := fileperm.Root(b.opts.ConfigPath); err != nil {
		return fmt.Errorf("bootstrap: own config: %w", err)
	}
	if b.opts.AccessPath != "" {
		if err := bootstrap.GrantSecret(b.opts.AccessPath, callerID); err != nil {
			return fmt.Errorf("bootstrap: grant secret role: %w", err)
		}
	}
	if err := b.mgr.Delete(); err != nil {
		return fmt.Errorf("bootstrap: delete state: %w", err)
	}
	return nil
}

// rollback removes the files this attempt created. A preserved identity
// (reset --keep-identity) is not ours to delete: removing it would
// destroy the very key the operator asked to retain, and it may be the
// only copy of that node's identity.
func (b *Bootstrap) rollback() {
	if !b.identityKept {
		_ = os.Remove(b.opts.IdentityPath)
	}
	_ = os.Remove(b.opts.ConfigPath)
}

// resultFor renders the public metadata plus the peer invite and the
// secret-bearing join string. Candidates are local endpoints plus a
// bounded (2s) STUN probe via allCandidates; a failed probe simply
// leaves local candidates, it never fails the bootstrap.
func (b *Bootstrap) resultFor(ctx context.Context, cfg conf.Config, id identity.Identity) (BootstrapResult, error) {
	candsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cands := wan.Merge(localCandidates(cfg), wan.Candidates(candsCtx, cfg))
	invite, err := identity.EncodeInvite(identity.Invite{ID: id.ID, PublicKey: id.PublicKey, Addrs: cands})
	if err != nil {
		return BootstrapResult{}, err
	}
	used := dedupeBootstrapOverlays([]netip.Addr{cfg.Node.OverlayIPv4}, cfg.Node.OverlayIPv4Prefix)
	join, err := identity.EncodeNetworkInvite(identity.NetworkInvite{
		Secret:    cfg.Node.NetworkSecret,
		ID:        id.ID,
		PublicKey: id.PublicKey,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
		UsedV4:    used,
		Addrs:     cands,
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{
		NodeID:     fmt.Sprintf("%016x", id.ID),
		PublicKey:  base64.StdEncoding.EncodeToString(id.PublicKey),
		Name:       cfg.Node.Name,
		Overlay:    cfg.Node.OverlayIPv4.String(),
		Invite:     invite,
		Join:       join,
		ConfigPath: b.opts.ConfigPath,
	}, nil
}

func dedupeBootstrapOverlays(addrs []netip.Addr, prefix netip.Prefix) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(addrs))
	out := addrs[:0]
	for _, addr := range addrs {
		if !addr.IsValid() || !prefix.Contains(addr) {
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}

// build validates and builds (but does not write) the config + identity
// for one bootstrap verb. It mirrors the validation of the legacy
// helper entry points without duplicating their writes or
// their service handling: the daemon is the writer here, and the
// service is already running (it is this process).
func (b *Bootstrap) build(ctx context.Context, verb string, req bootstrapRequest) (conf.Config, identity.Identity, error) {
	switch verb {
	case bootstrap.VerbInit:
		return b.buildInit(req)
	case bootstrap.VerbJoin:
		return b.buildJoin(req)
	case bootstrap.VerbImport:
		return b.buildImport(req)
	default:
		return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap: unknown verb %q", verb)
	}
}

func (b *Bootstrap) controlConfig(cfg *conf.Config) {
	// The service control plane, and nothing else: the legacy dev-TCP
	// listener and its bearer token are never enabled by a bootstrap.
	cfg.API.Listen = ""
	cfg.API.Token = nil
	cfg.API.Socket = b.opts.Socket
	cfg.API.Pipe = b.opts.Pipe
	cfg.Path = b.opts.ConfigPath
}

// existingIdentity loads an identity.key already on disk. That file is
// what `reset --keep-identity` preserves: the node must come back with
// the same peer ID and public key, so init/join reuse it instead of
// generating and writing a new one. A missing key means a fresh node.
// Any other error (wrong length, group/other-readable mode) fails the
// attempt before the token is burned, so the operator can fix the key
// and retry without a new elevation.
func (b *Bootstrap) existingIdentity() (identity.Identity, bool, error) {
	id, err := identity.Load(b.opts.IdentityPath)
	if err == nil {
		return id, true, nil
	}
	if os.IsNotExist(err) {
		return identity.Identity{}, false, nil
	}
	return identity.Identity{}, false, fmt.Errorf("bootstrap: preserved identity %s: %w", b.opts.IdentityPath, err)
}

func (b *Bootstrap) buildInit(req bootstrapRequest) (conf.Config, identity.Identity, error) {
	cfg := conf.Default()
	if req.Name != "" {
		if err := conf.ValidateName(req.Name); err != nil {
			return conf.Config{}, identity.Identity{}, err
		}
		cfg.Node.Name = req.Name
	} else {
		cfg.Node.Name = conf.DefaultNodeName()
	}
	if req.Port != 0 {
		cfg.Node.ListenPort = req.Port
	}
	if req.MTU != 0 {
		cfg.Node.MTU = req.MTU
	}
	if req.MDNS != nil {
		cfg.Discovery.MDNS = *req.MDNS
	}
	if req.STUN != nil {
		cfg.Discovery.STUN = *req.STUN
	}
	if req.STUNServer != "" {
		cfg.Discovery.STUNServer = req.STUNServer
	}
	if req.PrefixV4 != "" {
		p, err := netip.ParsePrefix(req.PrefixV4)
		if err != nil {
			return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap init: overlay prefix v4: %w", err)
		}
		cfg.Node.OverlayIPv4Prefix = p
	}
	if req.PrefixV6 != "" {
		p, err := netip.ParsePrefix(req.PrefixV6)
		if err != nil {
			return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap init: overlay prefix v6: %w", err)
		}
		cfg.Node.OverlayIPv6Prefix = p
	}
	addr, err := conf.NextFreeOverlay(cfg.Node.OverlayIPv4Prefix, nil)
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	cfg.Node.OverlayIPv4 = addr
	secret := make([]byte, identity.KeySize)
	if _, err := rand.Read(secret); err != nil {
		return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap init: random secret: %w", err)
	}
	cfg.Node.NetworkSecret = secret
	id, kept, err := b.existingIdentity()
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	b.identityKept = kept
	if !kept {
		id, err = identity.Generate()
		if err != nil {
			return conf.Config{}, identity.Identity{}, err
		}
	}
	b.controlConfig(&cfg)
	if err := conf.Validate(cfg); err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	return cfg, id, nil
}

func (b *Bootstrap) buildJoin(req bootstrapRequest) (conf.Config, identity.Identity, error) {
	if strings.TrimSpace(req.Invite) == "" {
		return conf.Config{}, identity.Identity{}, errors.New("bootstrap join: invite is required")
	}
	inv, err := identity.DecodeNetworkInvite(strings.TrimSpace(req.Invite))
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	id, kept, err := b.existingIdentity()
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	b.identityKept = kept
	if !kept {
		id, err = identity.Generate()
		if err != nil {
			return conf.Config{}, identity.Identity{}, err
		}
	}
	cfg := conf.Default()
	cfg.Node.NetworkSecret = inv.Secret
	cfg.Node.OverlayIPv4Prefix = inv.PrefixV4
	cfg.Node.OverlayIPv6Prefix = inv.PrefixV6
	cfg.Node.Name = conf.DefaultNodeName()
	addr, err := conf.NextFreeOverlayHashed(inv.PrefixV4, inv.UsedV4, id.ID)
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	cfg.Node.OverlayIPv4 = addr
	cfg.Peers = append(cfg.Peers, conf.Peer{ID: inv.ID, PublicKey: inv.PublicKey, Addrs: inv.Addrs, Dialer: true})
	b.controlConfig(&cfg)
	if err := conf.Validate(cfg); err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	return cfg, id, nil
}

func (b *Bootstrap) buildImport(req bootstrapRequest) (conf.Config, identity.Identity, error) {
	if strings.TrimSpace(req.Source) == "" {
		return conf.Config{}, identity.Identity{}, errors.New("bootstrap import: source is required")
	}
	cfg, id, err := loadImportSource(strings.TrimSpace(req.Source))
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	// reset --keep-identity followed by import must not clobber the
	// preserved key with the source's. The imported config is adopted;
	// the node keeps the identity the operator asked to retain.
	keptID, kept, err := b.existingIdentity()
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	b.identityKept = kept
	if kept {
		id = keptID
	}
	if cfg.Node.OverlayIPv4.IsValid() {
		if !cfg.Node.OverlayIPv4Prefix.Contains(cfg.Node.OverlayIPv4) {
			return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap import: overlay %s is not in prefix %s", cfg.Node.OverlayIPv4, cfg.Node.OverlayIPv4Prefix)
		}
	} else {
		addr, err := conf.NextFreeOverlay(cfg.Node.OverlayIPv4Prefix, nil)
		if err != nil {
			return conf.Config{}, identity.Identity{}, err
		}
		cfg.Node.OverlayIPv4 = addr
	}
	b.controlConfig(&cfg)
	if err := conf.Validate(cfg); err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	return cfg, id, nil
}

// Outcome returns the completed bootstrap's config, identity, and
// caller, for the in-process transition. It is only valid after a
// successful bootstrap endpoint call.
func (b *Bootstrap) Outcome() (BootstrapOutcome, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.done {
		return BootstrapOutcome{}, false
	}
	return BootstrapOutcome{Config: b.cfg, ID: b.id, CallerID: b.caller}, true
}
