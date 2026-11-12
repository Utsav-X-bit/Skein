package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// systemConfigDirs are the privileged config locations the GUI must never
// write, even when the user asks for a path inside one. The daemon (or the
// elevated helper) owns them; the GUI is unprivileged. The platform
// install root (app.SystemDir) is checked live by isSystemConfigDir via
// liveSystemDir, not stored here: on Android it is set from Java after
// startup, so a value captured at package init would predate it — and
// on Android the app-private files dir is app-owned, not privileged,
// so it is not a protected root at all there.
var systemConfigDirs = []string{
	`C:\Program Files\Skein`,
	"/usr",
	"/var",
}

// IdentityService exposes the daemon's public identity and creates an
// identity offline, for a config the user owns and no daemon is running on.
type IdentityService struct{}

// OfflineIdentity is the public half of an offline identity. The private key
// stays in identity.key on disk; it is never returned to the frontend.
type OfflineIdentity struct {
	PeerID       string `json:"peer_id"`
	PublicKey    string `json:"public_key"`
	IdentityPath string `json:"identity_path"`
}

// Public returns the running daemon's public identity (id, public key, name).
func (s *IdentityService) Public(ctx context.Context) (api.Identity, error) {
	c, err := ControlClient()
	if err != nil {
		return api.Identity{}, err
	}
	var out api.Identity
	err = c.do(ctx, "GET", "/api/v1/identity", nil, &out)
	return out, err
}

// GenerateOffline creates an identity in a directory the user owns, without
// a daemon and without elevation: it refuses a system path and an existing
// config, reuses an existing identity.key, and otherwise generates a key and
// writes it through configstore (0600, atomic, under the config lock).
func (s *IdentityService) GenerateOffline(ctx context.Context, targetDir string) (OfflineIdentity, error) {
	dir, err := offlineTargetDir(targetDir)
	if err != nil {
		return OfflineIdentity{}, err
	}
	configPath := filepath.Join(dir, "skein.toml")
	if _, err := os.Stat(configPath); err == nil {
		return OfflineIdentity{}, fmt.Errorf("identity: %s already exists; refusing to overwrite an existing config", configPath)
	} else if !os.IsNotExist(err) {
		return OfflineIdentity{}, fmt.Errorf("identity: %s: %w", configPath, err)
	}

	identityPath := filepath.Join(dir, "identity.key")
	if _, err := os.Stat(identityPath); err == nil {
		id, err := identity.Load(identityPath)
		if err != nil {
			return OfflineIdentity{}, fmt.Errorf("identity: %w", err)
		}
		return publicIdentity(id, identityPath), nil
	} else if !os.IsNotExist(err) {
		return OfflineIdentity{}, fmt.Errorf("identity: %s: %w", identityPath, err)
	}

	id, err := identity.Generate()
	if err != nil {
		return OfflineIdentity{}, fmt.Errorf("identity: %w", err)
	}
	store, err := configstore.Open(configPath)
	if err != nil {
		return OfflineIdentity{}, fmt.Errorf("identity: %w", err)
	}
	defer store.Close()
	if err := store.WriteIdentity(identityPath, id); err != nil {
		return OfflineIdentity{}, fmt.Errorf("identity: %w", err)
	}
	return publicIdentity(id, identityPath), nil
}

// publicIdentity projects the public half of id. Only these fields ever leave
// this package: no private key, not even a truncated form.
func publicIdentity(id identity.Identity, path string) OfflineIdentity {
	return OfflineIdentity{
		PeerID:       fmt.Sprintf("%016x", id.ID),
		PublicKey:    base64.StdEncoding.EncodeToString(id.PublicKey),
		IdentityPath: path,
	}
}

// offlineTargetDir validates the destination: absolute, outside the system
// config dirs, existing, a directory, resolved through symlinks, and not
// world-writable. The denylist runs first so the refusal never depends on the
// protected directory existing on this host. The actual write still needs the
// OS to permit it, so ownership is enforced by the kernel, not guessed from
// mode bits.
func offlineTargetDir(targetDir string) (string, error) {
	if !filepath.IsAbs(targetDir) {
		return "", fmt.Errorf("identity: target %q must be an absolute path", targetDir)
	}
	dir := filepath.Clean(targetDir)
	if isSystemConfigDir(dir) {
		return "", fmt.Errorf("identity: refusing to write the system config directory %s; choose a directory you own", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("identity: target %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("identity: target %s is not a directory", dir)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("identity: target %s: %w", dir, err)
	}
	if isSystemConfigDir(resolved) {
		return "", fmt.Errorf("identity: refusing to write the system config directory %s; choose a directory you own", resolved)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o002 != 0 {
		return "", fmt.Errorf("identity: target %s is world-writable; choose a directory only you can write", resolved)
	}
	return resolved, nil
}

// isSystemConfigDir reports whether dir is one of the privileged config
// directories or below it. Each root is compared both as written and
// symlink-resolved, so a platform that redirects a root (darwin's /etc ->
// /private/etc) cannot be used to slip past the refusal. Windows paths
// compare case-insensitively.
func isSystemConfigDir(dir string) bool {
	roots := systemConfigDirs
	if sys := liveSystemDir(); sys != "" {
		roots = append([]string{sys}, roots...)
	}
	for _, sys := range roots {
		if isUnder(sys, dir) {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(sys); err == nil && isUnder(resolved, dir) {
			return true
		}
	}
	return false
}

// isUnder reports whether path is root itself or a descendant of it.
func isUnder(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	if runtime.GOOS == "windows" {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
