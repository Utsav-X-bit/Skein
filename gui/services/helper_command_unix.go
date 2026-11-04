//go:build !windows

package services

import (
	"context"
	"os/exec"
)

func defaultHelperPath() string { return "/usr/libexec/skein-helper" }

// polkitActionForVerb maps a minimal-helper verb to the polkit action
// shipped in gui/build/linux/nfpm/org.skein.helper.policy (Step 11).
// The policy pins /usr/libexec/skein-helper, so pkexec refuses any
// other binary under these action IDs, and the prompt names the
// action ("install its system service", "change who may control it")
// instead of a bare binary name (Agent C L-16). Verbs with no
// action (status is unprivileged and never reaches pkexec) return "".
// Unknown verbs return "" too: they fail at the helper's own verb
// check rather than at a made-up polkit action.
func polkitActionForVerb(verb string) string {
	switch verb {
	case "install", "bootstrap":
		return "org.skein.helper.install"
	case "start", "stop", "restart", "uninstall":
		return "org.skein.helper.lifecycle"
	case "grant-user", "set-owner", "revoke-user":
		return "org.skein.helper.grants"
	default:
		return ""
	}
}

// helperCommand builds the elevated helper invocation. With an
// action for the verb it passes --action, so the prompt is the named
// policy action; without one it falls back to pkexec's generic
// action (only reachable for verbs the policy does not cover —
// today none of the retained verbs, since status never elevates).
// Fail-closed note: if the shipped policy is absent (unpackaged
// dev tree), pkexec --action refuses with "action not found"
// instead of silently elevating under the generic action. That is
// the intended behaviour for a packaged install; dev flows use
// SKEIN_HELPER_NO_PKEXEC=1 with a local helper.
func helperCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	// M6 (F-04): the no-pkexec bypass is dev-only (securityEnv
	// returns "" in release builds), so a release GUI always
	// elevates through pkexec.
	if noPkexecOverride() {
		return exec.CommandContext(ctx, path, args...)
	}
	if len(args) > 0 {
		if action := polkitActionForVerb(args[0]); action != "" {
			return exec.CommandContext(ctx, "pkexec", append([]string{"--action", action, path}, args...)...)
		}
	}
	return exec.CommandContext(ctx, "pkexec", append([]string{path}, args...)...)
}
