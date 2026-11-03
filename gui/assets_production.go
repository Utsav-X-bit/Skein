//go:build production

package main

import "embed"

// The Wails production build embeds the compiled frontend. wails3 build
// passes -tags production and runs the frontend build first.
//
//go:embed all:frontend/dist
var assets embed.FS
