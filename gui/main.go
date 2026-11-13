package main

import (
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// mainOnce keeps the Android entry point idempotent (see main).
var mainOnce sync.Once

// main serves as the application entry point. It creates the app, the main
// window, and registers the tray/updater hooks (W10), then blocks until exit.
//
// On Android this function is not called by the Go runtime: the Wails
// runtime calls it from WailsBridge.nativeInit, which runs on every
// Activity onCreate. Android can recreate the Activity while the process
// (and this runtime) lives on, so the second call would build a second App
// — registering every service and event twice — and the event bus panics on
// the duplicate registration. mainOnce makes the entry point idempotent:
// a recreation only rebinds the WebView bridge (nativeInit does that before
// calling us) and leaves the running app alone.
func main() {
	mainOnce.Do(run)
}

func run() {
	a := New()

	// Use native window decorations everywhere. The Windows custom titlebar
	// never exposed minimize/close controls, and native decorations are the
	// expected behavior on Windows 10.
	frameless := false

	// Start maximised on every platform: a fixed 1280x800 window is taller
	// than the work area on common 1366x768 laptops, which clipped the
	// bottom of the app. Maximising fits whatever screen is present while
	// the 900x600 minimum still governs small layouts.
	startState := application.WindowStateMaximised

	a.window = a.App.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Skein",
		URL:              "/",
		Width:            1280,
		Height:           800,
		MinWidth:         900,
		MinHeight:        600,
		StartState:       startState,
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
		if quitting.Load() {
			return
		}
		if trayAvailable() {
			a.window.Hide()
			e.Cancel()
			return
		}
		// No tray to reopen from: closing the window must end the process,
		// otherwise the single-instance handler leaves a headless app behind.
		quitting.Store(true)
		a.Quit()
	})

	if os.Getenv("SKEIN_NO_TRAY") == "" {
		registerTray(a)
	}
	registerUpdater(a)

	// Fullscreen is a Linux-only presentation choice; Windows keeps a normal
	// resizable window with the custom titlebar.
	if runtime.GOOS == "linux" {
		go func() {
			time.Sleep(700 * time.Millisecond)
			if w := a.Window(); w != nil {
				w.Fullscreen()
			}
		}()
	}

	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
