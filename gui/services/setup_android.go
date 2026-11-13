//go:build android

package services

import (
	"context"
	"errors"

	"github.com/Utsav-X-bit/skein/internal/android"
)

func init() {
	// On Android the GUI and the daemon are the same process under
	// the app's own UID, so there is no helper to mint a bootstrap
	// token through. Minting is in-process instead. EnsureBootstrap
	// (below) mints the token for the verb the user is about to run
	// and stores it; this override returns that exact token, because
	// bootstrap.Mint refuses a second mint while one is valid
	// ("already in progress"). If no token was pre-minted — a flow
	// that skipped EnsureBootstrap, or a retry after a failed
	// attempt tore the server down (SetupService cancels on
	// bootstrapCall failure) — re-ensure the server first: it mints
	// fresh and serves the endpoint the post needs. Without the
	// re-ensure, the retry would mint a token no server accepts.
	mintBootstrapToken = func(_ context.Context, verb string) (string, error) {
		if tok, ok := android.TakeBootstrapToken(verb); ok {
			return tok, nil
		}
		if err := android.EnsureBootstrap(verb); err != nil {
			return "", err
		}
		if tok, ok := android.TakeBootstrapToken(verb); ok {
			return tok, nil
		}
		return "", errors.New("android: bootstrap token unavailable after ensure")
	}
	// The in-process bootstrap server lives in internal/android.
	ensureBootstrapImpl = func(_ context.Context, verb string) error {
		return android.EnsureBootstrap(verb)
	}
	cancelBootstrapImpl = func(_ context.Context) error {
		return android.StopBootstrap()
	}
}
