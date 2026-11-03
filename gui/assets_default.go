//go:build !production

package main

import "embed"

// Plain go test/build has no compiled frontend in a clean checkout. Wails
// production builds use assets_production.go; wails3 dev serves the Vite
// frontend instead of these embedded assets.
var assets embed.FS
