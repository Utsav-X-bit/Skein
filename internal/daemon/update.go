// Step 7 (ADR-001 §6.4): daemon self-update. The daemon verifies a
// signed manifest, stages the new binary, and applies it through
// Step 6's UpgradeDaemon/RollbackDaemon — never through a helper
// binary swap, and never by rewriting its own binary in-process
// (on Windows a running service cannot overwrite its own exe, and
// on Linux swapping under a running process and replying from the
// same process is equally unsafe). Apply is therefore split:
//
//   - UpdateCheck / UpdateApply (endpoint, this process): verify
//     only, then launch a detached copy of skeind (apply-update
//     subcommand, hidden from --help) and return immediately.
//   - ApplyUpdateDetached (detached process): upgrade, start,
//     15 s health check, rollback on failure, and write the
//     outcome to the update state file for UpdateCheck to report.
//
// Security fixes over the old helper update path (Agent E F-05):
// downgrade protection (new version must be strictly greater),
// basename-only manifest binary resolved under the staging dir
// (a full path is accepted only under the staging dir, with a
// deprecation log — the plan's migration, to be removed in the
// next major version), key_id + valid_until required and covered
// by the signature (so an attacker cannot extend a key's life by
// editing plaintext fields), and a symlink-safe manifest open
// (O_NOFOLLOW on Unix; Lstat + regular-file check everywhere).
//
// The release public key is duplicated from internal/app/update.go
// (the legacy helper path, deleted in Step 12). Until then the
// two copies must stay identical; Step 12 deletes the app copy.
// Tests override updateVerifyKeys (same-package
// only) with generated keypairs — production code never does.
package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/service"
	"github.com/Utsav-X-bit/skein/internal/version"
)

// updateVerifyKeys is the trusted release-key set for daemon update
// manifests (M6, Agent E F-05 key rotation). Each entry is a public
// half only; private halves live with the release pipeline. v1's
// seed is sha512("skein daemon update release key v1").
//
// Rotation procedure (no PKI, no in-band key distribution — a key
// is trusted only when a shipped binary contains its public half):
//  1. ADD: a release ships a binary whose map contains both the old
//     and the new key id, delivered by a manifest signed with the
//     OLD key (so binaries that only know the old key accept it).
//     During this overlap both keys are trusted.
//  2. USE: the release pipeline then signs manifests with the new
//     key. Old binaries reject them (unknown key id) — that is
//     why step 1 must ship first; it is the upgrade path, not a
//     downgrade.
//  3. RETIRE: a later release ships a binary whose map omits the
//     old key id. Manifests signed with the retired key are then
//     rejected as unknown, even if their valid_until has not
//     passed. valid_until (signed, per manifest) is the second
//     retirement mechanism: a compromised key's manifests expire
//     even before the key is removed from the map, provided the
//     pipeline stops signing with it and sets short valid_until
//     values during the transition.
//
// Tests replace this map (same-package) with throwaway keypairs.
var updateVerifyKeys = map[string]ed25519.PublicKey{
	"v1": mustDecodeUpdateKey("VcDhzP6paP1T24kvn5cxUp/Q3gCSNAOx+aD3cKJ+e2k="),
}

func mustDecodeUpdateKey(b64 string) ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) != ed25519.PublicKeySize {
		panic("daemon: bad update release key")
	}
	return ed25519.PublicKey(key)
}

// updateKeyID is the current release key id. It is NOT the only
// trusted id: verification looks the manifest's key_id up in
// updateVerifyKeys, so a rotation overlap (v1 + v2) is trusted
// without a code change beyond adding the new public key to the
// map in the release that introduces it.
const updateKeyID = "v1"

// UpdateManifest is the daemon update manifest. Binary is a
// basename resolved under the staging dir (see verifyUpdate).
// Signature is base64 ed25519 over
// version\nbinary\nsha256\nkey_id\nvalid_until, using the exact
// manifest strings (so valid_until cannot be extended without
// re-signing).
type UpdateManifest struct {
	Version    string `json:"version"`
	Binary     string `json:"binary"`
	SHA256     string `json:"sha256"`
	Signature  string `json:"signature"`
	KeyID      string `json:"key_id"`
	ValidUntil string `json:"valid_until"`
}

// SignedPayload is the exact byte string the signature covers.
func (m UpdateManifest) SignedPayload() string {
	return m.Version + "\n" + m.Binary + "\n" + m.SHA256 + "\n" + m.KeyID + "\n" + m.ValidUntil
}

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(s string) (semver, error) {
	s = strings.TrimPrefix(s, "v")
	if i := strings.Index(s, "+"); i >= 0 {
		s = s[:i]
	}
	var v semver
	if i := strings.Index(s, "-"); i >= 0 {
		v.pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("version %q is not X.Y.Z", s)
	}
	nums := []*int{&v.major, &v.minor, &v.patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("version %q: bad component %q", s, p)
		}
		*nums[i] = n
	}
	return v, nil
}

// compareSemver returns -1/0/+1. A release outranks its own
// pre-release (1.0.0 > 1.0.0-dev); pre-release strings compare
// lexically, which is enough for the downgrade gate (the gate
// only needs a total order, not full semver precedence).
func compareSemver(a, b semver) int {
	if a.major != b.major {
		return cmpInt(a.major, b.major)
	}
	if a.minor != b.minor {
		return cmpInt(a.minor, b.minor)
	}
	if a.patch != b.patch {
		return cmpInt(a.patch, b.patch)
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	default:
		return strings.Compare(a.pre, b.pre)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// currentVersion is version.Version stripped of build metadata
// (Info() appends "+revision time", which is not semver).
func currentVersion() string {
	v := version.Version
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, " "); i >= 0 {
		v = v[:i]
	}
	return v
}

func expectedBinaryName() string {
	if runtime.GOOS == "windows" {
		return "skeind.exe"
	}
	return "skeind"
}

// updateService is the service-manager surface for update apply /
// rollback. Production uses the real service package; tests swap
// it for a fake. It mirrors service.UpgradeDaemon /
// RollbackDaemon / Start so the daemon never calls Install /
// Rewrite directly (Step 6 single-registration rule).
type updateService interface {
	Upgrade(newPath, configPath string) (service.Registration, error)
	Rollback(reg service.Registration, configPath string) error
	Start() error
}

type realUpdateService struct{}

func (realUpdateService) Upgrade(newPath, configPath string) (service.Registration, error) {
	return service.UpgradeDaemon(newPath, configPath)
}

func (realUpdateService) Rollback(reg service.Registration, configPath string) error {
	return service.RollbackDaemon(reg, configPath)
}

func (realUpdateService) Start() error { return service.Start() }

func (d *Daemon) updateSvc() updateService {
	if d.updateSvcOverride != nil {
		return d.updateSvcOverride
	}
	return realUpdateService{}
}

func (d *Daemon) updateLauncher() func(exe string, args []string, logPath string) error {
	if d.updateLauncherOverride != nil {
		return d.updateLauncherOverride
	}
	return spawnDetached
}

// stagingDir resolves the update staging directory: the test
// override, else the installed service binary's directory, else
// the running exe's directory (dev runs with no service).
func (d *Daemon) stagingDir() (string, error) {
	if d.stagingDirOverride != "" {
		return d.stagingDirOverride, nil
	}
	if exe, _, installed, err := service.Installed(); err == nil && installed && exe != "" {
		return filepath.Dir(exe), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("daemon: update staging: %w", err)
	}
	return filepath.Dir(exe), nil
}

// updateStatePath is the state file the detached apply process
// writes and UpdateCheck reads. It is update state, not config:
// configstore never writes it, and deleting it loses only the
// last-update report, never membership.
func (d *Daemon) updateStatePath() string {
	cfg := d.config()
	if cfg.Path == "" {
		return ""
	}
	return cfg.Path + ".update-state.json"
}

func writeUpdateState(path string, st api.UpdateState) error {
	if path == "" {
		return errors.New("daemon: update state needs a config path")
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeBytesAtomic(path, raw, 0o600)
}

func readUpdateState(path string) (*api.UpdateState, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var st api.UpdateState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// verifyUpdateManifest opens the manifest symlink-safely, checks
// every field, verifies the signature and the staged binary's
// SHA-256, and enforces downgrade protection. It returns the
// manifest and the resolved staged binary path. The staged
// binary is validated with service.ValidateDaemonPath (absolute,
// regular non-symlink, non-world-writable parent, skeind
// basename), so a verified hash alone never registers a planted
// binary in a writable directory.
func verifyUpdateManifest(manifestPath, stagingDir string, stagedOverride string) (UpdateManifest, string, error) {
	raw, err := readManifestFile(manifestPath)
	if err != nil {
		return UpdateManifest{}, "", err
	}
	var m UpdateManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return UpdateManifest{}, "", fmt.Errorf("update: manifest: %w", err)
	}
	if m.Version == "" || m.Binary == "" || m.SHA256 == "" || m.Signature == "" {
		return UpdateManifest{}, "", errors.New("update: manifest needs version, binary, sha256, and signature")
	}
	// M6 (F-05): key_id must be a member of the trusted set, not
	// necessarily the current key — that is what makes rotation
	// possible. An unknown id is rejected before any signature
	// check, so a retired key's manifests fail even with a valid
	// signature and an unexpired valid_until.
	key, ok := updateVerifyKeys[m.KeyID]
	if !ok {
		return UpdateManifest{}, "", fmt.Errorf("update: manifest key_id %q is not a trusted release key (retired or unknown)", m.KeyID)
	}
	if m.ValidUntil == "" {
		return UpdateManifest{}, "", errors.New("update: manifest needs valid_until (key expiry)")
	}
	expiry, err := time.Parse(time.RFC3339, m.ValidUntil)
	if err != nil {
		return UpdateManifest{}, "", fmt.Errorf("update: valid_until: %w", err)
	}
	if !time.Now().Before(expiry) {
		return UpdateManifest{}, "", fmt.Errorf("update: manifest key expired at %s", m.ValidUntil)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return UpdateManifest{}, "", fmt.Errorf("update: signature: %w", err)
	}
	if !ed25519.Verify(key, []byte(m.SignedPayload()), sig) {
		return UpdateManifest{}, "", errors.New("update: signature does not match the release key")
	}
	// Downgrade protection (plan §6.4, mandatory, no flag):
	// the new version must be strictly greater than the running
	// version. A genuine downgrade is a package reinstall, not
	// an API call.
	cur, err := parseSemver(currentVersion())
	if err != nil {
		return UpdateManifest{}, "", fmt.Errorf("update: current version: %w", err)
	}
	next, err := parseSemver(m.Version)
	if err != nil {
		return UpdateManifest{}, "", fmt.Errorf("update: manifest version: %w", err)
	}
	if compareSemver(next, cur) <= 0 {
		return UpdateManifest{}, "", &api.StatusError{
			Status:      http.StatusConflict,
			Code:        api.ErrCodeConflict,
			Message:     fmt.Sprintf("update: version %s is not newer than running %s; downgrades are refused", m.Version, currentVersion()),
			Remediation: "to downgrade, reinstall the older package explicitly",
		}
	}
	// Binary resolution: basename-only under the staging dir.
	// Migration (plan §6.4): a full path is accepted only when
	// it is already under the staging dir and passes validation,
	// with a deprecation log; it is removed in the next major
	// version, and the release pipeline must sign basenames.
	var staged string
	if stagedOverride != "" {
		staged = stagedOverride
	} else if filepath.Base(m.Binary) == m.Binary {
		staged = filepath.Join(stagingDir, m.Binary)
	} else {
		clean := filepath.Clean(m.Binary)
		rel, err := filepath.Rel(stagingDir, clean)
		if err != nil || strings.HasPrefix(rel, "..") {
			return UpdateManifest{}, "", fmt.Errorf("update: manifest binary %q is not a basename under the staging dir %s", m.Binary, stagingDir)
		}
		log.Printf("skein: update: manifest binary %q uses a full path (deprecated; release pipeline must sign basenames)", m.Binary)
		staged = clean
	}
	if err := service.ValidateDaemonPath(staged); err != nil {
		return UpdateManifest{}, "", fmt.Errorf("update: staged binary: %w", err)
	}
	if err := verifyBinaryHash(staged, m.SHA256); err != nil {
		return UpdateManifest{}, "", err
	}
	return m, staged, nil
}

func verifyBinaryHash(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != strings.ToLower(want) {
		return fmt.Errorf("update: binary digest is %s, manifest says %s", got, want)
	}
	return nil
}

// UpdateCheck implements GET /api/v1/system/update/check: verify
// the manifest (signature, hash, key expiry, downgrade) without
// swapping, restarting, or touching the service. It also reports
// the last update outcome from the state file (written by the
// detached apply process) or the in-memory pending state.
func (d *Daemon) UpdateCheck(ctx context.Context, manifestPath string) (api.UpdateCheckResult, error) {
	_ = ctx
	staging, err := d.stagingDir()
	if err != nil {
		return api.UpdateCheckResult{}, err
	}
	m, _, err := verifyUpdateManifest(manifestPath, staging, "")
	if err != nil {
		return api.UpdateCheckResult{}, err
	}
	var last *api.UpdateState
	if st, err := readUpdateState(d.updateStatePath()); err == nil && st != nil {
		last = st
	} else {
		d.sysMu.Lock()
		last = d.updateState
		d.sysMu.Unlock()
	}
	return api.UpdateCheckResult{
		Version:         m.Version,
		CurrentVersion:  currentVersion(),
		Checked:         true,
		UpdateAvailable: true,
		LastUpdate:      last,
	}, nil
}

// UpdateApply implements POST /api/v1/system/update/apply: verify
// (same gates as check), then launch the detached apply process
// and return immediately. The caller polls UpdateCheck for the
// outcome; Applied is never claimed here.
func (d *Daemon) UpdateApply(ctx context.Context, manifestPath, stagedBinaryPath string, confirm bool) (api.UpdateApplyResult, error) {
	if err := requireConfirm(confirm, "update apply"); err != nil {
		return api.UpdateApplyResult{}, err
	}
	_ = ctx
	cfg := d.config()
	if cfg.Path == "" {
		return api.UpdateApplyResult{}, errors.New("daemon: update apply needs a config path")
	}
	staging, err := d.stagingDir()
	if err != nil {
		return api.UpdateApplyResult{}, err
	}
	m, staged, err := verifyUpdateManifest(manifestPath, staging, stagedBinaryPath)
	if err != nil {
		return api.UpdateApplyResult{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return api.UpdateApplyResult{}, fmt.Errorf("daemon: update apply: %w", err)
	}
	statePath := d.updateStatePath()
	args := []string{"apply-update", "--staged", staged, "--config", cfg.Path, "--state", statePath}
	logPath := filepath.Join(staging, "apply-update.log")
	if err := d.updateLauncher()(exe, args, logPath); err != nil {
		return api.UpdateApplyResult{}, fmt.Errorf("daemon: update apply: launch detached process: %w", err)
	}
	d.sysMu.Lock()
	d.updateState = &api.UpdateState{Version: m.Version, AtMS: time.Now().UnixMilli()}
	d.sysMu.Unlock()
	return api.UpdateApplyResult{Version: m.Version, Checked: true, Detached: true, Staged: staged}, nil
}

// ApplyUpdateDetached is the detached apply process (skeind
// apply-update, hidden from --help): upgrade via Step 6, start,
// 15 s health check, rollback on failure, and write the outcome
// to the state file. It never deletes backups on failure; the
// .old binary and config backup survive for hand recovery, and
// successful updates prune older update backups to last-3.
func ApplyUpdateDetached(staged, configPath, statePath string, svc updateService) error {
	if svc == nil {
		svc = realUpdateService{}
	}
	return applyUpdateInternal(staged, configPath, statePath, svc, func() bool {
		return serviceHealthy(15 * time.Second)
	})
}

func applyUpdateInternal(staged, configPath, statePath string, svc updateService, healthy func() bool) error {
	reg, err := svc.Upgrade(staged, configPath)
	if err != nil {
		_ = writeUpdateState(statePath, api.UpdateState{Error: err.Error(), AtMS: time.Now().UnixMilli()})
		return fmt.Errorf("update: upgrade: %w", err)
	}
	fail := func(err error) error {
		rbErr := svc.Rollback(reg, configPath)
		if rbErr != nil {
			_ = writeUpdateState(statePath, api.UpdateState{Error: fmt.Sprintf("apply failed (%v); rollback also failed (%v); backups at %s, %s were not deleted", err, rbErr, reg.BinaryBackupPath, reg.ConfigBackupPath), AtMS: time.Now().UnixMilli()})
			return fmt.Errorf("update: apply failed (%v); rollback also failed (%v); backups at %s, %s were not deleted", err, rbErr, reg.BinaryBackupPath, reg.ConfigBackupPath)
		}
		_ = svc.Start()
		_ = writeUpdateState(statePath, api.UpdateState{RolledBack: true, Error: err.Error(), Backup: reg.ConfigBackupPath, AtMS: time.Now().UnixMilli()})
		return fmt.Errorf("update: %w (rolled back)", err)
	}
	if err := svc.Start(); err != nil {
		return fail(fmt.Errorf("start: %w", err))
	}
	if !healthy() {
		return fail(errors.New("health check: service did not reach running state"))
	}
	pruneBackupFiles(configPath, "upgrade", reg.ConfigBackupPath)
	_ = writeUpdateState(statePath, api.UpdateState{Applied: true, Backup: reg.ConfigBackupPath, AtMS: time.Now().UnixMilli()})
	return nil
}

// serviceHealthy polls the service state until Running or the
// deadline, mirroring the old helper's 15 s health check.
func serviceHealthy(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		st, err := service.ReadStatus()
		if err == nil && st.State == service.StateRunning {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}
