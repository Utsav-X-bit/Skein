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
