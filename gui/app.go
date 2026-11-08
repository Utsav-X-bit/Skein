package main

import (
	_ "embed"
	"github.com/Utsav-X-bit/skein/gui/services"
	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed build/appicon.png
var appIcon []byte

// App holds the application and its main window so services and hooks can
// reach both without globals.
type App struct {
	*application.App
	window *application.WebviewWindow
}

// New creates the application with the bound services and registers the
// typed events the frontend subscribes to.
func New() *App {
	application.RegisterEvent[api.Connection](services.EventConnectionChanged)
	application.RegisterEvent[[]api.Peer](services.EventPeersChanged)
	application.RegisterEvent[[]api.Route](services.EventRoutesChanged)
	application.RegisterEvent[api.Activity](services.EventActivitySample)
	application.RegisterEvent[api.LogEntry](services.EventLogEntry)
	application.RegisterEvent[api.ServiceStatus](services.EventServiceChanged)
	application.RegisterEvent[services.UpdateInfo](services.EventUpdateAvailable)
	application.RegisterEvent[api.ProbeResult](services.EventPeerProbe)
	application.RegisterEvent[api.Event](services.EventPeerUp)
	application.RegisterEvent[api.Event](services.EventPeerJoined)
	application.RegisterEvent[api.Event](services.EventPeerDiscovered)
	application.RegisterEvent[api.Event](services.EventPeerAdded)
	application.RegisterEvent[api.Event](services.EventPeerRemoved)
	application.RegisterEvent[api.Event](services.EventPeerDown)
	application.RegisterEvent[api.Event](services.EventPeerDeparted)
	application.RegisterEvent[api.Event](services.EventDialerFallback)
	application.RegisterEvent[api.Event](services.EventPeerLeave)
	application.RegisterEvent[api.Event](services.EventPeerRejoined)
	application.RegisterEvent[api.Event](services.EventNameChanged)
	application.RegisterEvent[api.Event](services.EventSecretRotated)
	application.RegisterEvent[api.Event](services.EventSystemReset)
	application.RegisterEvent[api.Event](services.EventSystemImported)
	application.RegisterEvent[api.Event](services.EventLeaveDataplaneStopFailed)

	var a *App
	a = &App{
		App: application.New(application.Options{
			Name:        "Skein Desktop",
			Description: "Control surface for a Skein mesh",
			Icon:        appIcon,
			Services:    services.AllServices(),
			Assets: application.AssetOptions{
				Handler: application.AssetFileServerFS(assets),
			},
			Linux: application.LinuxOptions{
				ApplicationID:                 "com.skein.desktop",
				ProgramName:                   "skein-desktop",
				DisableQuitOnLastWindowClosed: true,
			},
			SingleInstance: &application.SingleInstanceOptions{
				UniqueID: "com.skein.desktop",
				OnSecondInstanceLaunch: func(application.SecondInstanceData) {
					if a.window != nil {
						a.window.UnMinimise()
						a.window.Show()
						a.window.Focus()
					}
				},
			},
		}),
	}
	return a
}

// SetWindow records the main window.
func (a *App) SetWindow(w *application.WebviewWindow) {
	a.window = w
}

// Window returns the main window.
func (a *App) Window() *application.WebviewWindow {
	return a.window
}

// Emit sends a typed event to all windows.
func (a *App) Emit(name string, payload any) {
	a.App.Event.Emit(name, payload)
}
