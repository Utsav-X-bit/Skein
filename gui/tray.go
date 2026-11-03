package main

import (
	"log"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var (
	// tray is the live system tray, or nil when the tray is unavailable.
	tray *application.SystemTray
	// quitting distinguishes a real quit (tray Quit) from a close-to-tray.
	quitting atomic.Bool
)

// trayAvailable reports whether a system tray was registered.
func trayAvailable() bool {
	return tray != nil
}

// registerTray creates the system tray and its menu.
//
// ponytail: W4 hook — W10 replaces this with the full tray (status dot,
// connect/disconnect, peer submenu, notifications).
func registerTray(a *App) {
	tray = a.SystemTray.New()
	tray.SetIcon(appIcon)
	tray.SetTooltip("Skein")

	open := func() {
		w := a.Window()
		if w == nil {
			return
		}
		w.UnMinimise()
		w.Show()
		w.Focus()
	}
	tray.OnClick(open)

	menu := a.NewMenu()
	menu.Add("Open Skein").OnClick(func(*application.Context) { open() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		quitting.Store(true)
		a.Quit()
	})
	tray.SetMenu(menu)

	log.Println("system tray registered")
}
