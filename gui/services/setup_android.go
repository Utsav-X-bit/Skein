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
	// The in-process bootstrap server lives in internal/android.
	ensureBootstrapImpl = func(_ context.Context, verb string) error {
		return android.EnsureBootstrap(verb)
	}
	cancelBootstrapImpl = func(_ context.Context) error {
		return android.StopBootstrap()
	}
}
