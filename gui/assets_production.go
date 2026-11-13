//go:build production || android

package main

import "embed"

// Installed Android builds always embed the frontend, including debug APKs:
// there is no external Vite server on the phone.
//
//go:embed all:frontend/dist
var assets embed.FS
