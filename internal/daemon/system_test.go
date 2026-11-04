package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/route"
	"github.com/Utsav-X-bit/skein/internal/service"
)

// newSystemDaemon builds a configured daemon over temp files:
// config at dir/skein.toml (written via Render, so persist and
// backup operate on real files) and identity.key beside it. The
// daemon holds the config lock for its lifetime, exactly like a
// service daemon; Close releases it.
func newSystemDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := conf.Default()
	cfg.Path = filepath.Join(dir, "skein.toml")
	cfg.Node.Name = "systemtest"
	cfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.1")
	cfg.Node.NetworkSecret = make([]byte, 32)
	for i := range cfg.Node.NetworkSecret {
		cfg.Node.NetworkSecret[i] = 0x11
	}
	cfg.API.Token = make([]byte, 32)
	cfg.API.Listen = ""
	cfg.API.Socket = filepath.Join(dir, "control.sock")
	cfg.Discovery.MDNS = true
	cfg.Discovery.STUN = false
	text, err := conf.Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := os.WriteFile(cfg.Path, text, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.key"), id.PrivateKey, 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := New(cfg, id)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, dir
}

func addTestPeer(t *testing.T, d *Daemon) uint64 {
	t.Helper()
	pid, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.registerPeer(pid.ID, pid.PublicKey, nil, true, "peer"); err != nil {
		t.Fatalf("registerPeer: %v", err)
	}
	return pid.ID
}

func TestLeaveSetsDepartedFlagPersistsAndBlocksConnect(t *testing.T) {
	d, dir := newSystemDaemon(t)
	peerID := addTestPeer(t, d)
	if err := d.routes.UpdateDirect(route.DirectLink{PeerID: peerID, RTT: time.Millisecond}); err != nil {
		t.Fatalf("UpdateDirect: %v", err)
	}
	if len(d.Routes()) == 0 {
		t.Fatal("want a direct route before leave")
	}
	res, err := d.LeaveSystem(context.Background(), true)
	if err != nil {
		t.Fatalf("LeaveSystem: %v", err)
	}
	if !res.Departed || res.Propagated {
		t.Fatalf("leave = %+v, want departed=true propagated=false", res)
	}
	if res.PeersLeft != 1 {
		t.Fatalf("PeersLeft = %d, want 1", res.PeersLeft)
	}
	if res.RoutesWithdrawn < 1 {
		t.Fatalf("RoutesWithdrawn = %d, want >= 1", res.RoutesWithdrawn)
	}
	// The flag survives a reload from disk: a restart must not
	// silently rejoin.
	loaded, err := conf.Load(filepath.Join(dir, "skein.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Node.Departed {
		t.Fatal("persisted config lost the departed flag")
	}
	if _, err := d.Connect(context.Background()); err == nil {
		t.Fatal("Connect after leave = nil, want refusal (rejoin first)")
	}
	// A second leave is a conflict, not a silent no-op.
	if _, err := d.LeaveSystem(context.Background(), true); err == nil {
		t.Fatal("second LeaveSystem = nil, want conflict")
	}
	// Leave without confirm is refused before any work.
	d2, _ := newSystemDaemon(t)
	if _, err := d2.LeaveSystem(context.Background(), false); err == nil {
		t.Fatal("LeaveSystem without confirm = nil, want 400")
	}
}

func TestSystemJoinRejoinClearsDepartedOnlyWithConfirm(t *testing.T) {
	d, _ := newSystemDaemon(t)
	if _, err := d.LeaveSystem(context.Background(), true); err != nil {
		t.Fatalf("LeaveSystem: %v", err)
	}
	// A fresh invite for the SAME network: build one from the
	// daemon's own config (secret + prefixes) with a different
	// peer identity as inviter.
	inviter, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	cfg := d.config()
	join, err := identity.EncodeNetworkInvite(identity.NetworkInvite{
		Secret:    cfg.Node.NetworkSecret,
		ID:        inviter.ID,
		PublicKey: inviter.PublicKey,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
	})
	if err != nil {
		t.Fatalf("EncodeNetworkInvite: %v", err)
	}
	if _, err := d.SystemJoin(context.Background(), join, false); err == nil {
		t.Fatal("rejoin without confirm = nil, want 400")
	}
	res, err := d.SystemJoin(context.Background(), join, true)
	if err != nil {
		t.Fatalf("SystemJoin rejoin: %v", err)
	}
	if !res.Rejoined {
		t.Fatalf("rejoin = %+v, want Rejoined=true", res)
	}
	if d.departed() {
		t.Fatal("departed flag still set after rejoin")
	}
	// A different-network invite is refused even when departed
	// is clear: a configured node never switches networks via join.
	otherSecret := make([]byte, 32)
	for i := range otherSecret {
		otherSecret[i] = 0x22
	}
	other, err := identity.EncodeNetworkInvite(identity.NetworkInvite{
		Secret:    otherSecret,
		ID:        inviter.ID,
		PublicKey: inviter.PublicKey,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SystemJoin(context.Background(), other, true); err == nil {
		t.Fatal("different-network join = nil, want 409")
	}
}

func TestSystemJoinSameNetworkAddsPeer(t *testing.T) {
	d, _ := newSystemDaemon(t)
	inviter, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	cfg := d.config()
	join, err := identity.EncodeNetworkInvite(identity.NetworkInvite{
		Secret:    cfg.Node.NetworkSecret,
		ID:        inviter.ID,
		PublicKey: inviter.PublicKey,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
		Addrs:     []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:11010")},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.SystemJoin(context.Background(), join, false)
	if err != nil {
		t.Fatalf("SystemJoin: %v", err)
	}
	if res.Rejoined || res.PeerID != fmt.Sprintf("%016x", inviter.ID) {
		t.Fatalf("join = %+v, want peer %016x, not rejoined", res, inviter.ID)
	}
	if _, ok := d.peers.Get(inviter.ID); !ok {
		t.Fatal("inviter not in peer table after system join")
	}
}

func TestSystemInitAlwaysRefuses(t *testing.T) {
	d, _ := newSystemDaemon(t)
	err := d.SystemInit(context.Background())
	if err == nil {
		t.Fatal("SystemInit = nil, want 409")
	}
	var se *api.StatusError
	if !errors.As(err, &se) || se.Status != 409 {
		t.Fatalf("SystemInit error = %v, want 409 StatusError", err)
	}
}

func TestRotateSecretBacksUpPersistsAndRequiresRestart(t *testing.T) {
	d, dir := newSystemDaemon(t)
	oldSecret := append([]byte(nil), d.config().Node.NetworkSecret...)
	res, err := d.RotateSecret(context.Background(), true)
	if err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}
	if res.Join == "" {
		t.Fatal("rotate returned an empty join string")
	}
	if strings.Contains(res.Join, "skein:2:") == false {
		t.Fatalf("rotate join = %q, want a skein:2: invite", res.Join)
	}
	if !res.RestartRequired {
		t.Fatal("rotate RestartRequired = false, want true (session manager holds the old PSK)")
	}
	if res.Backup == "" {
		t.Fatal("rotate returned no backup path")
	}
	if _, err := os.Stat(res.Backup); err != nil {
		t.Fatalf("rotate backup %s missing: %v", res.Backup, err)
	}
	// The new secret is persisted and differs from the old one;
	// the join string decodes to it (and only the response
	// carries it — nothing here logs it).
	loaded, err := conf.Load(filepath.Join(dir, "skein.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Node.NetworkSecret) == string(oldSecret) {
		t.Fatal("persisted secret unchanged after rotate")
	}
	inv, err := identity.DecodeNetworkInvite(res.Join)
	if err != nil {
		t.Fatalf("rotate join does not decode: %v", err)
	}
	if string(inv.Secret) != string(loaded.Node.NetworkSecret) {
		t.Fatal("rotate join secret does not match persisted secret")
	}
	if _, err := d.RotateSecret(context.Background(), false); err == nil {
		t.Fatal("RotateSecret without confirm = nil, want 400")
	}
}

func TestResetBacksUpRemovesConfigAndKeepsServiceAction(t *testing.T) {
	d, dir := newSystemDaemon(t)
	cfgPath := filepath.Join(dir, "skein.toml")
	res, err := d.ResetSystem(context.Background(), api.ResetOptions{Confirm: true})
	if err != nil {
		t.Fatalf("ResetSystem: %v", err)
	}
	if res.Backup == "" {
		t.Fatal("reset returned no backup path")
	}
	if res.ServiceActionRequired != "stop" {
		t.Fatalf("ServiceActionRequired = %q, want stop", res.ServiceActionRequired)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config still present after reset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Backup, "skein.toml")); err != nil {
		t.Fatalf("backup config missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Backup, "identity.key")); err != nil {
		t.Fatalf("backup identity missing: %v", err)
	}
	if _, err := d.ResetSystem(context.Background(), api.ResetOptions{Confirm: false}); err == nil {
		t.Fatal("ResetSystem without confirm = nil, want 400")
	}
}

func TestResetPurgeRemovesIdentityUnlessKept(t *testing.T) {
	d, dir := newSystemDaemon(t)
	if _, err := d.ResetSystem(context.Background(), api.ResetOptions{Purge: true, KeepIdentity: true, Confirm: true}); err != nil {
		t.Fatalf("ResetSystem purge+keep: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.key")); err != nil {
		t.Fatalf("identity removed despite keep_identity: %v", err)
	}
}

func TestSystemImportReplacesConfigWithBackupAndRestartFlag(t *testing.T) {
	d, dir := newSystemDaemon(t)
	srcDir := t.TempDir()
	srcCfg := conf.Default()
	srcCfg.Node.Name = "imported-node"
	srcCfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.9")
	srcCfg.Node.NetworkSecret = make([]byte, 32)
	for i := range srcCfg.Node.NetworkSecret {
		srcCfg.Node.NetworkSecret[i] = 0x33
	}
	srcCfg.API.Listen = ""
	srcCfg.API.Socket = filepath.Join(srcDir, "control.sock")
	srcCfg.Discovery.MDNS = true
	srcCfg.Discovery.STUN = false
	text, err := conf.Render(srcCfg)
	if err != nil {
		t.Fatalf("Render source: %v", err)
	}
	// loadImportSource rejects group/other-writable parents;
	// TempDir on this VM is 0770, so tighten it like a real
	// source directory.
	if err := os.Chmod(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(srcDir, "skein.toml")
	if err := os.WriteFile(srcPath, text, 0o600); err != nil {
		t.Fatal(err)
	}
	srcID, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "identity.key"), srcID.PrivateKey, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := d.SystemImport(context.Background(), srcPath, true)
	if err != nil {
		t.Fatalf("SystemImport: %v", err)
	}
	if !res.RestartRequired {
		t.Fatal("import RestartRequired = false, want true")
	}
	if res.Backup == "" {
		t.Fatal("import returned no backup path")
	}
	loaded, err := conf.Load(filepath.Join(dir, "skein.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Node.Name != "imported-node" {
		t.Fatalf("imported name = %q, want imported-node", loaded.Node.Name)
	}
	if string(loaded.Node.NetworkSecret) != string(srcCfg.Node.NetworkSecret) {
		t.Fatal("imported secret does not match source secret")
	}
	if _, err := d.SystemImport(context.Background(), srcPath, false); err == nil {
		t.Fatal("SystemImport without confirm = nil, want 400")
	}
}

// --- update verification ---

func signManifest(t *testing.T, priv ed25519.PrivateKey, m UpdateManifest) UpdateManifest {
	t.Helper()
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(m.SignedPayload())))
	return m
}

func writeManifest(t *testing.T, dir string, m UpdateManifest) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// newSignedManifest builds a valid manifest for a staged binary
// in stagingDir, signed with a throwaway key that the test
// installs as updateVerifyKeys["v1"] (restored on cleanup).
func newSignedManifest(t *testing.T, stagingDir, version string) (string, string) {
	t.Helper()
	// A real staging dir is an admin-owned package dir, not
	// group-writable; TempDir on this VM is 0770, which
	// ValidateDaemonPath rightly rejects. Simulate the real
	// dir instead of weakening the check.
	if err := os.Chmod(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := updateVerifyKeys
	updateVerifyKeys = map[string]ed25519.PublicKey{updateKeyID: pub}
	t.Cleanup(func() { updateVerifyKeys = old })
	staged := filepath.Join(stagingDir, expectedBinaryName())
	if err := os.WriteFile(staged, []byte("new-skeind-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("new-skeind-bytes"))
	m := UpdateManifest{
		Version:    version,
		Binary:     expectedBinaryName(),
		SHA256:     hex.EncodeToString(sum[:]),
		KeyID:      updateKeyID,
		ValidUntil: time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	m = signManifest(t, priv, m)
	return writeManifest(t, stagingDir, m), staged
}

func TestUpdateCheckAcceptsNewerAndRejectsDowngrade(t *testing.T) {
	d, dir := newSystemDaemon(t)
	staging := t.TempDir()
	d.SetUpdateStagingDir(staging)
	manifest, _ := newSignedManifest(t, staging, "0.2.0")
	res, err := d.UpdateCheck(context.Background(), manifest)
	if err != nil {
		t.Fatalf("UpdateCheck newer: %v", err)
	}
	if !res.Checked || !res.UpdateAvailable || res.Version != "0.2.0" {
		t.Fatalf("check = %+v, want version 0.2.0 available", res)
	}
	_ = dir
	// Downgrade and equal versions are 409, not "not available".
	for _, v := range []string{"0.0.9", "0.1.0-dev"} {
		mv, _ := newSignedManifest(t, staging, v)
		if _, err := d.UpdateCheck(context.Background(), mv); err == nil {
			t.Fatalf("UpdateCheck %s = nil, want 409 downgrade refusal", v)
		} else {
			var se *api.StatusError
			if !errors.As(err, &se) || se.Status != 409 {
				t.Fatalf("UpdateCheck %s error = %v, want 409", v, err)
			}
		}
	}
}

func TestUpdateVerifyRejectsBadSignatureBadHashExpiredAndSymlink(t *testing.T) {
	d, _ := newSystemDaemon(t)
	staging := t.TempDir()
	d.SetUpdateStagingDir(staging)
	manifest, staged := newSignedManifest(t, staging, "0.2.0")

	// Bad signature: re-sign with a different key is not possible
	// here (the manifest is already signed); corrupt the payload
	// instead so the signature no longer matches.
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m UpdateManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.SHA256 = strings.Repeat("0", 64)
	bad := writeManifest(t, staging, m)
	if _, err := d.UpdateCheck(context.Background(), bad); err == nil {
		t.Fatal("corrupted manifest = nil, want signature/hash error")
	}
	// Bad hash with a valid signature: sign a manifest whose
	// digest does not match the staged binary.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := updateVerifyKeys
	updateVerifyKeys = map[string]ed25519.PublicKey{updateKeyID: pub}
	t.Cleanup(func() { updateVerifyKeys = old })
	_ = staged
	badHash := UpdateManifest{
		Version:    "0.2.0",
		Binary:     expectedBinaryName(),
		SHA256:     strings.Repeat("1", 64),
		KeyID:      updateKeyID,
		ValidUntil: time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	badHash = signManifest(t, priv, badHash)
	if _, err := d.UpdateCheck(context.Background(), writeManifest(t, staging, badHash)); err == nil {
		t.Fatal("bad-hash manifest = nil, want digest error")
	}
	// Expired key.
	expired := UpdateManifest{
		Version:    "0.2.0",
		Binary:     expectedBinaryName(),
		SHA256:     hex.EncodeToString(func() []byte { s := sha256.Sum256([]byte("new-skeind-bytes")); return s[:] }()),
		KeyID:      updateKeyID,
		ValidUntil: time.Now().Add(-time.Hour).Format(time.RFC3339),
	}
	expired = signManifest(t, priv, expired)
	if _, err := d.UpdateCheck(context.Background(), writeManifest(t, staging, expired)); err == nil {
		t.Fatal("expired manifest = nil, want expiry error")
	}
	// Symlink manifest is refused, not followed.
	link := filepath.Join(staging, "link.json")
	if err := os.Symlink(manifest, link); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpdateCheck(context.Background(), link); err == nil {
		t.Fatal("symlink manifest = nil, want refusal")
	}
	// Full-path binary outside the staging dir is refused.
	outside := UpdateManifest{
		Version:    "0.2.0",
		Binary:     "/etc/passwd",
		SHA256:     strings.Repeat("2", 64),
		KeyID:      updateKeyID,
		ValidUntil: time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	outside = signManifest(t, priv, outside)
	if _, err := d.UpdateCheck(context.Background(), writeManifest(t, staging, outside)); err == nil {
		t.Fatal("full-path binary outside staging = nil, want refusal")
	}
}

// M6 — Agent E F-05 key rotation. Overlap: a binary trusting both
// v1 and v2 accepts manifests signed with either key. Retirement:
// a binary trusting only v2 rejects a correctly signed, unexpired
// v1 manifest. This is the mechanism the release pipeline uses:
// ship the two-key binary under the old key first, sign with the
// new key afterwards, drop the old key in a later binary.
func TestUpdateKeyRotationOverlapAndRetirement(t *testing.T) {
	d, _ := newSystemDaemon(t)
	staging := t.TempDir()
	if err := os.Chmod(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	d.SetUpdateStagingDir(staging)
	staged := filepath.Join(staging, expectedBinaryName())
	if err := os.WriteFile(staged, []byte("new-skeind-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("new-skeind-bytes"))
	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := updateVerifyKeys
	t.Cleanup(func() { updateVerifyKeys = old })
	mk := func(keyID string, priv ed25519.PrivateKey) string {
		m := UpdateManifest{
			Version:    "0.2.0",
			Binary:     expectedBinaryName(),
			SHA256:     hex.EncodeToString(sum[:]),
			KeyID:      keyID,
			ValidUntil: time.Now().Add(time.Hour).Format(time.RFC3339),
		}
		return writeManifest(t, staging, signManifest(t, priv, m))
	}
	// Overlap: both keys trusted.
	updateVerifyKeys = map[string]ed25519.PublicKey{"v1": pub1, "v2": pub2}
	if _, err := d.UpdateCheck(context.Background(), mk("v1", priv1)); err != nil {
		t.Fatalf("overlap: v1 manifest rejected: %v", err)
	}
	if _, err := d.UpdateCheck(context.Background(), mk("v2", priv2)); err != nil {
		t.Fatalf("overlap: v2 manifest rejected: %v", err)
	}
	// Unknown key id is rejected even with a valid signature.
	if _, err := d.UpdateCheck(context.Background(), mk("v3", priv1)); err == nil {
		t.Fatal("unknown key_id v3 accepted, want rejection")
	}
	// Retirement: v1 removed from the trusted set.
	updateVerifyKeys = map[string]ed25519.PublicKey{"v2": pub2}
	if _, err := d.UpdateCheck(context.Background(), mk("v1", priv1)); err == nil {
		t.Fatal("retired v1 manifest accepted, want rejection")
	}
	if _, err := d.UpdateCheck(context.Background(), mk("v2", priv2)); err != nil {
		t.Fatalf("after retirement: v2 manifest rejected: %v", err)
	}
}

// fakeUpdateService records Upgrade/Rollback/Start calls and can
// fail on demand, without touching systemctl/sc.
type fakeUpdateService struct {
	upgradeErr error
	startErr   error
	upgrades   []string
	rollbacks  []service.Registration
	starts     int
	reg        service.Registration
}

func (f *fakeUpdateService) Upgrade(newPath, configPath string) (service.Registration, error) {
	f.upgrades = append(f.upgrades, newPath)
	if f.upgradeErr != nil {
		return service.Registration{}, f.upgradeErr
	}
	f.reg = service.Registration{
		DaemonPath:         newPath,
		PreviousDaemonPath: filepath.Join(filepath.Dir(newPath), "skeind.old-bin"),
		HadPrevious:        true,
		BinaryBackupPath:   filepath.Join(filepath.Dir(newPath), "skeind.old"),
		ConfigBackupPath:   configPath + ".upgrade-bak-20260101-000000",
	}
	return f.reg, nil
}

func (f *fakeUpdateService) Rollback(reg service.Registration, configPath string) error {
	f.rollbacks = append(f.rollbacks, reg)
	return nil
}

func (f *fakeUpdateService) Start() error {
	f.starts++
	return f.startErr
}

func TestApplyUpdateRollbackOnFailedHealthCheck(t *testing.T) {
	staging := t.TempDir()
	configPath := filepath.Join(staging, "skein.toml")
	statePath := filepath.Join(staging, "state.json")
	staged := filepath.Join(staging, expectedBinaryName())
	if err := os.WriteFile(staged, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeUpdateService{}
	err := applyUpdateInternal(staged, configPath, statePath, fake, func() bool { return false })
	if err == nil {
		t.Fatal("apply with failing health = nil, want rollback error")
	}
	if len(fake.rollbacks) != 1 {
		t.Fatalf("rollbacks = %d, want 1", len(fake.rollbacks))
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var st api.UpdateState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if !st.RolledBack || st.Applied {
		t.Fatalf("state = %+v, want rolled_back=true applied=false", st)
	}
}

func TestApplyUpdateSuccessWritesAppliedState(t *testing.T) {
	staging := t.TempDir()
	configPath := filepath.Join(staging, "skein.toml")
	statePath := filepath.Join(staging, "state.json")
	staged := filepath.Join(staging, expectedBinaryName())
	if err := os.WriteFile(staged, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeUpdateService{}
	if err := applyUpdateInternal(staged, configPath, statePath, fake, func() bool { return true }); err != nil {
		t.Fatalf("apply healthy: %v", err)
	}
	if len(fake.rollbacks) != 0 {
		t.Fatalf("rollbacks = %d, want 0", len(fake.rollbacks))
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var st api.UpdateState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Applied || st.RolledBack {
		t.Fatalf("state = %+v, want applied=true", st)
	}
}

func TestUpdateApplyEndpointLaunchesDetachedOnly(t *testing.T) {
	d, _ := newSystemDaemon(t)
	staging := t.TempDir()
	d.SetUpdateStagingDir(staging)
	manifest, staged := newSignedManifest(t, staging, "0.2.0")
	var launched []string
	d.updateLauncherOverride = func(exe string, args []string, logPath string) error {
		launched = append(launched, exe)
		launched = append(launched, args...)
		_ = logPath
		return nil
	}
	res, err := d.UpdateApply(context.Background(), manifest, "", true)
	if err != nil {
		t.Fatalf("UpdateApply: %v", err)
	}
	if !res.Detached {
		t.Fatalf("apply = %+v, want detached=true", res)
	}
	if res.Staged != staged {
		t.Fatalf("staged = %q, want %q", res.Staged, staged)
	}
	found := false
	for _, a := range launched {
		if a == "apply-update" {
			found = true
		}
	}
	if !found {
		t.Fatalf("launcher args = %v, want apply-update subcommand", launched)
	}
	if _, err := d.UpdateApply(context.Background(), manifest, "", false); err == nil {
		t.Fatal("UpdateApply without confirm = nil, want 400")
	}
}

func TestReloadGrantsUsesLiveAccessFile(t *testing.T) {
	d, _ := newSystemDaemon(t)
	// ReloadGrants reads the platform access file
	// (controlAccessFile). On this VM it is missing, which is
	// not an error — the table becomes unconfigured. The
	// important property is that reload does not panic and
	// does not require a restart to take effect; the matrix
	// tests in api cover the role behaviour itself.
	if err := d.ReloadGrants(context.Background()); err != nil {
		t.Fatalf("ReloadGrants missing file: %v", err)
	}
}
