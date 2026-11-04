//go:build !skeindev

package services

// securityEnv is the release-build implementation: security
// variables are ignored, unconditionally. See env.go (M6, F-04).
// A dev build (-tags skeindev) uses env_dev.go instead.
func securityEnv(string) string { return "" }
