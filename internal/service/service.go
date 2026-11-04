// Package service installs and removes the Skein system service and reads
// its state from the OS (systemd or the Windows SCM) without elevation.
package service

// Name is the service name used by systemd and the Windows SCM.
const Name = "skein"

// State is the observed service state.
type State string

const (
	// StateMissing is the service not registered.
	StateMissing State = "missing"
	// StateStopped is the service registered and not running.
	StateStopped State = "stopped"
	// StateStarting is the service in its start transition.
	StateStarting State = "starting"
	// StateStopping is the service in its stop transition.
	StateStopping State = "stopping"
	// StateRunning is the service running.
	StateRunning State = "running"
	// StateError is the service registered in a failed state.
	StateError State = "error"
	// StateUnknown is the registered state that could not be read.
	StateUnknown State = "unknown"
)

// Status is the service state read without elevation.
type Status struct {
	State   State `json:"state"`
	Enabled bool  `json:"enabled"`
}

// Status reads the service state without elevation, so the GUI can report
// it while the daemon is stopped.
func ReadStatus() (Status, error) { return status() }

// Start begins the service. It needs elevation (helper verbs).
func Start() error { return start() }

// Stop stops the service. It needs elevation (helper verbs).
func Stop() error { return stop() }

// Restart restarts the service. It needs elevation (helper verbs).
func Restart() error { return restart() }

// Install registers the daemon as a system service. Step 6: the
// candidate is validated (skeind only, regular non-symlink file,
// non-world-writable parent) before any registration write, so a
// direct caller cannot bypass RegisterDaemon's validation. Prefer
// RegisterDaemon for installs/upgrades: it adds stop-first,
// backups, and restore-on-failure around Install/Rewrite.
func Install(executable, configPath string) error {
	if err := ValidateDaemonPath(executable); err != nil {
		return err
	}
	return install(executable, configPath)
}

// Uninstall removes the service registration.
func Uninstall() error { return uninstall() }

// Installed reports the executable and config path the registered service
// runs. ok is false when the service is not registered.
func Installed() (executable, configPath string, ok bool, err error) { return installed() }

// Rewrite points a registered service at a new executable and config path
// (config import). The service must be stopped; a missing registration is
// an error (use Install). Step 6: the new executable is validated
// before the rewrite — the argument is the NEW binary, never the
// already-installed exe (that was the Agent D upgrade bug).
func Rewrite(executable, configPath string) error {
	if err := ValidateDaemonPath(executable); err != nil {
		return err
	}
	return rewrite(executable, configPath)
}
