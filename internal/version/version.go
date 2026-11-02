// Package version reports the build version.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is the release line. The exact build is identified by the VCS
// revision that Info appends, so every commit produces a different string.
const Version = "0.1.0-dev"

// Info returns Version plus the commit and build time that go build embeds
// from the working tree. A build without VCS metadata (module zip, tests)
// reports the release line alone.
func Info() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	var revision, built string
	dirty := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.time":
			built = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return Version
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	out := Version + "+" + revision
	if dirty {
		out += ".dirty"
	}
	if built != "" {
		out += " " + strings.TrimSpace(built)
	}
	return out
}
