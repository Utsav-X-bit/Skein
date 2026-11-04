// Package fileperm restricts secret-bearing files to the accounts that need
// them: the owner, plus SYSTEM and Administrators on Windows so the service can
// read its own configuration.
package fileperm

// Restrict makes path readable only by its owner (Unix) or by the owner,
// SYSTEM, and Administrators (Windows).
func Restrict(path string) error { return restrict(path) }

// ChownToSudoUser gives path to the user who invoked sudo, so files created by
// an elevated command stay usable by the normal user.
func ChownToSudoUser(path string) error { return chownToSudoUser(path) }

// PreserveOwner makes path owned like ref. When ref does not exist, the sudo
// user owns it instead. The daemon uses it when rewriting the config as root.
func PreserveOwner(path, ref string) error { return preserveOwner(path, ref) }

// Root restricts a root-owned file or directory to root: 0600 root:root (0750
// for directories) on Unix, or a service-only DACL with full control for
// SYSTEM and Administrators on Windows. The helper init/import and the
// daemon persistence path use it; the GUI never needs these files.
func Root(path string) error { return root(path) }

// GrantUser rewrites path's access to include user (a user name or SID) with
// full control, keeping SYSTEM and Administrators. On Windows this is the
// service-only DACL writer with the single interactive user added. On Unix it
// is unsupported: socket access is granted with an ACL, not file ownership.
func GrantUser(path, user string) error { return grantUser(path, user) }

// DirAdminOnly reports whether dir's DACL grants write-like access
// only to administrators (SYSTEM, Administrators, TrustedInstaller).
// On Unix it reports whether the directory is not group- or
// other-writable. The service uses it to accept daemon parents
// outside %ProgramFiles% that are still admin-only.
func DirAdminOnly(dir string) (bool, error) { return dirAdminOnly(dir) }

// NoWorldAccess reports whether no well-known non-administrator
// principal (Everyone, Authenticated Users, Users) has any access to
// path. On Unix it reports whether the mode grants nothing to group
// or other. Tests use it where Windows mode bits are meaningless.
func NoWorldAccess(path string) (bool, error) { return noWorldAccess(path) }
