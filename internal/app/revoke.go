// Revoke-user support (M5 Step 10): remove a grant from the access file
// and reload the live daemon's grants, so a revoked caller loses access
// on its next call without a daemon restart.
//
// The access file is NOT config (Step 3 gate exception, same as grant):
// it is the uid:role / SID:role grant list the daemon loads at
// construction and on POST /api/v1/system/grants/reload (Step 7, secret
// role). This file therefore has its own writer here, not configstore.
//
// Safety rules, all enforced in revokeGrantFromFile before any write:
//   - The file must exist and parse cleanly; a malformed file is a
//     fail-closed error and is never rewritten.
//   - Revoking a uid/SID with no grant is a clean error, never a
//     silent success (the caller must know the revoke did nothing).
//   - Removing the last grant in the file, or removing a secret grant
//     when no secret grant would remain, requires --force: an empty
//     file reloads as "configured, no grants" (every non-root kernel
//     caller denied), which is fail-closed but a bigger hammer than
//     removing one grant from a multi-user file. The file is kept
//     (never deleted) after the last removal: deleting it would
//     reload as "unconfigured", which grants kernel callers read
//     access — a revoked user would regain reads.
//   - The rewritten file keeps every other line byte-identical
//     (comments and blank lines included) and is written atomically
//     (temp + 0600 + rename in the same directory). The helper runs
//     elevated, so the new file is owned by root/SYSTEM exactly as
//     the old one was; no chown is attempted, which also keeps this
//     writer usable in unprivileged tests.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// RevokeOutcome describes what a revoke did. Warning is non-empty when
// the revoke needed --force; Reloaded reports whether the live daemon
// re-read the file (false when the daemon is not running, in which
// case the file change takes effect at next start).
type RevokeOutcome struct {
	Identity   string
	Role       string // role the revoked grant held ("" = bare line, read by API interpretation)
	Warning    string
	Reloaded   bool
	ReloadNote string
}

// accessGrant is one parsed grant line.
type accessGrant struct {
	identity string
	role     string
}

// parseAccessLine splits one non-blank, non-comment access-file line.
// A line with a colon must end in a known role; anything else is
// malformed (fail closed, never silently reinterpreted). This mirrors
// grantUserWithRole's parser and api.ParseAccessGrants' role set.
func parseAccessLine(line string) (accessGrant, error) {
	if i := strings.LastIndex(line, ":"); i >= 0 {
		role := line[i+1:]
		switch role {
		case "read", "mutate", "secret":
			return accessGrant{identity: line[:i], role: role}, nil
		default:
			return accessGrant{}, fmt.Errorf("malformed access line %q (want <uid|SID>[:read|mutate|secret])", line)
		}
	}
	return accessGrant{identity: line, role: ""}, nil
}

// revokeGrantFromFile removes identity from the access file at path.
// It returns an error and writes nothing when the file is missing,
// malformed, or does not contain the identity, or when the removal
// needs --force and force is false.
func revokeGrantFromFile(path, identity string, force bool) (RevokeOutcome, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RevokeOutcome{}, fmt.Errorf("revoke-user: no grants file at %s (nothing granted)", path)
		}
		return RevokeOutcome{}, fmt.Errorf("revoke-user: read %s: %w", path, err)
	}
	var kept []string
	var removed *accessGrant
	secretLeft := 0
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			kept = append(kept, raw)
			continue
		}
		g, err := parseAccessLine(line)
		if err != nil {
			return RevokeOutcome{}, fmt.Errorf("revoke-user: %s: %w", path, err)
		}
		if g.identity == identity {
			if removed != nil {
				return RevokeOutcome{}, fmt.Errorf("revoke-user: %s has duplicate lines for %q; refusing to guess", path, identity)
			}
			cp := g
			removed = &cp
			continue
		}
		kept = append(kept, raw)
		if g.role == "secret" {
			secretLeft++
		}
	}
	if removed == nil {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: %q has no grant in %s", identity, path)
	}
	out := RevokeOutcome{Identity: identity, Role: removed.role}
	grantsLeft := 0
	for _, raw := range kept {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		grantsLeft++
	}
	if !force {
		switch {
		case grantsLeft == 0:
			return RevokeOutcome{}, fmt.Errorf("revoke-user: %q is the last grant in %s; re-run with --force (afterwards every non-root caller is denied until a new grant is added)", identity, path)
		case removed.role == "secret" && secretLeft == 0:
			return RevokeOutcome{}, fmt.Errorf("revoke-user: %q is the last secret grant in %s; re-run with --force", identity, path)
		}
	}
	if grantsLeft == 0 || (removed.role == "secret" && secretLeft == 0) {
		out.Warning = "last grant removed: every non-root caller is denied until a new grant is added (root/SYSTEM keep implicit secret access)"
		if removed.role == "secret" && secretLeft == 0 && grantsLeft > 0 {
			out.Warning = "last secret grant removed: no granted user can manage secrets until a new secret grant is added (root/SYSTEM keep implicit secret access)"
		}
	}
	joined := strings.Join(kept, "\n")
	if joined != "" && !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	if err := writeAccessFileAtomic(path, []byte(joined)); err != nil {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: write %s: %w", path, err)
	}
	return out, nil
}

// writeAccessFileAtomic writes the access file atomically (temp file in
// the same directory, 0600, fsync, rename). It is deliberately not
// writeAtomicBytes: that helper chowns to root, which an unprivileged
// test cannot do, and the helper runs elevated in production, so the
// temp file already has the right owner.
//
// An explicit DACL keeps replacement grants scoped to their creator,
// SYSTEM, and Administrators. set-owner then adds the interactive user.
func writeAccessFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Normalize any pre-existing grants DACL before replacement.
	// Report real failures; absence is normal on the first write.
	if err := fileperm.Restrict(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("repair %s: %w", path, err)
	}
	return os.Rename(name, path)
}

// accessRoles reads the access file into an identity -> role map for
// authorisation checks. A missing file means no grants (only the
// implicit root/SYSTEM secret holder exists); a malformed file fails
// closed.
func accessRoles(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	roles := map[string]string{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		g, err := parseAccessLine(line)
		if err != nil {
			return nil, err
		}
		roles[g.identity] = g.role
	}
	return roles, nil
}
