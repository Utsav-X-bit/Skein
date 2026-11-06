package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// GrantSecret writes callerID into the access file with the secret
// role, preserving every other grant's role. It is the bootstrap
// grant: the user who just installed the node can invite and rotate
// without a second elevation. It deliberately does not touch the
// socket/pipe ACL: the daemon (re)applies ACLs from the access file
// when it creates its control listener, and the post-transition
// daemon loads this file at construction, so the live server's
// authorisation table already contains the grant — the caller also
// applies it directly to the live server via Daemon.GrantSecret, so a
// grant is never "written but not yet effective" in either direction.
// The access file carries identities and roles only, never secrets.
func GrantSecret(accessPath, callerID string) error {
	if callerID == "" || callerID == "unknown" || callerID == "bearer" {
		return fmt.Errorf("bootstrap: cannot grant secret to caller %q", callerID)
	}
	grants := map[string]api.Role{}
	if raw, err := os.ReadFile(accessPath); err == nil {
		parsed, err := api.ParseAccessGrants(strings.NewReader(string(raw)))
		if err != nil {
			return fmt.Errorf("bootstrap: access file %s: %w", accessPath, err)
		}
		grants = parsed
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: access file %s: %w", accessPath, err)
	}
	grants[callerID] = api.RoleSecret
	keys := make([]string, 0, len(grants))
	for k := range grants {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		// api grant keys are "uid:<n>" for numeric uids and a bare
		// SID otherwise; the file format is "<uid>:role" /
		// "<SID>:role", so the "uid:" prefix is stripped back off.
		id := strings.TrimPrefix(k, "uid:")
		fmt.Fprintf(&b, "%s:%s\n", id, grants[k].String())
	}
	// Reuse the bootstrap atomic writer (0600, temp + rename) so a
	// half-written grant file can never strand the daemon.
	return writeGrantFile(accessPath, []byte(b.String()))
}

func writeGrantFile(path string, data []byte) error {
	// The temp file goes in the target's own directory: a rename
	// cannot cross volumes, and the directory the file lands in is
	// what its DACL is inherited from. Splitting on "/" put the temp
	// in the process CWD on Windows (backslash paths), so the access
	// file inherited the CWD's DACL instead of the system
	// directory's.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".access-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	// The DACL is set first and explicitly. CreateTemp already creates
	// the file 0600 on Unix, but on Windows set-owner replaces the
	// system directory's DACL with protected, non-inheritable ACEs, so
	// a temp file created there inherits nothing (deny-all) — and even
	// Chmod, which is attribute-based on Windows, would then be
	// refused. Without this the renamed access file is deny-all and
	// the daemon cannot read its own grants on the next start.
	// Restrict grants the creator, SYSTEM, and Administrators, like
	// the bootstrap state writer does.
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
	// A destination left deny-all by the pre-fix writers cannot be
	// replaced: the rename needs DELETE on it, and an empty DACL grants
	// nobody DELETE (the owner only gets implicit READ_CONTROL and
	// WRITE_DAC). Restrict is the repair, not an optimisation: a real
	// failure is reported here rather than left to the rename. A
	// missing destination is the normal first write.
	if err := fileperm.Restrict(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: repair %s: %w", path, err)
	}
	return os.Rename(name, path)
}
