//go:build android

package services

import (
	"context"

	"github.com/Utsav-X-bit/skein/internal/android"
	"github.com/Utsav-X-bit/skein/internal/app"
)

func init() {
	// On Android the GUI and the daemon are the same process under
	// the app's own UID, so there is no helper to mint a bootstrap
	// token through. Minting is in-process instead. EnsureBootstrap
	// (below) mints the token for the verb the user is about to run
	// and stores it; this override returns that exact token, because
	// bootstrap.Mint refuses a second mint while one is valid
	// ("already in progress"). If no token was pre-minted — a flow
	// that skipped EnsureBootstrap — mint fresh; the bootstrap
	// server must already be running for the post to land.
	mintBootstrapToken = func(_ context.Context, verb string) (string, error) {
		if tok, ok := android.TakeBootstrapToken(verb); ok {
			return tok, nil
		}
		return app.MintBootstrapToken(verb)
	}
}

// EnsureBootstrap prepares the in-process bootstrap server for verb
// ("init", "join" or "import"). The mobile Onboarding calls it after
// the user picks create/join and before submitting the form, so the
// bootstrap endpoint is up when SetupService.Init/Join posts. It
// mints the single-use token and serves the daemon's bootstrap-only
// API over the in-process transport — the Android equivalent of the
// desktop helper minting a token and starting the service in
// bootstrap mode.
func (s *SetupService) EnsureBootstrap(ctx context.Context, verb string) error {
	_ = ctx
	return android.EnsureBootstrap(verb)
}

// CancelBootstrap stops an abandoned onboarding bootstrap server
// and releases the config-store lock it holds, so a later Connect
// can start the normal daemon. A successful bootstrap stops itself.
func (s *SetupService) CancelBootstrap(ctx context.Context) error {
	_ = ctx
	return android.StopBootstrap()
}
