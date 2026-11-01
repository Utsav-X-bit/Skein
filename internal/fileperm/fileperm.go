// Package fileperm restricts secret-bearing files to the accounts that need
// them: the owner, plus SYSTEM and Administrators on Windows so the service can
// read its own configuration.
package fileperm

// Restrict makes path readable only by its owner (Unix) or by the owner,
// SYSTEM, and Administrators (Windows).
func Restrict(path string) error { return restrict(path) }
