package main

import (
	"log"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// main serves as the application entry point. It creates the app, the main
// window, and registers the tray/updater hooks (W10), then blocks until exit.
func main() {
	a := New()

	// Native window decorations on Linux; a custom titlebar on Windows
	// (WindowControls in the command bar).
	frameless := runtime.GOOS == "windows"

	a.window = a.App.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Skein",
		URL:              "/",
		Width:            1440,
		Height:           900,
		MinWidth:         900,
		MinHeight:        600,
		Frameless:        frameless,
		BackgroundColour: application.NewRGB(8, 9, 11),
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 56,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	// Closing the window hides to the tray when one is available; otherwise
	// it closes for real. The service keeps running either way.
	a.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !quitting.Load() && trayAvailable() {
			a.window.Hide()
			e.Cancel()
		}
	})

	registerTray(a)
	registerUpdater(a)

	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
