package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/firewall"
)

// accessFile is where the IPC layer persists granted caller uids, one per
// line (W1-owned format).
const accessFile = "/etc/skein/access"

// Security implements api.GUI: ownership and permission facts the GUI shows
// as notices. No secrets.
func (d *Daemon) Security() api.SecurityStatus {
	cfg := d.config()
	identityPath := filepath.Join(filepath.Dir(cfg.Path), "identity.key")
	st := api.SecurityStatus{
		ConfigPath:      cfg.Path,
		ConfigOwner:     fileOwner(cfg.Path),
		ConfigMode:      fileMode(cfg.Path),
		IdentityOwner:   fileOwner(identityPath),
		IdentityMode:    fileMode(identityPath),
		SocketPath:      cfg.API.Socket,
		UpdateSignature: "unknown", // the daemon does not sign itself; the helper's update-daemon does
		FirewallManager: firewall.Manager(),
	}
	if st.SocketPath == "" {
		st.SocketPath = cfg.API.Pipe
	}
	st.AllowedUIDs = grantedUIDs()
	return st
}

func fileMode(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.Mode().String()
}

// grantedUIDs reads the persisted granted-uid list from the IPC layer.
func grantedUIDs() []uint32 {
	if runtime.GOOS == "windows" {
		return nil // Windows grants are SIDs, persisted by the pipe layer
	}
	raw, err := os.ReadFile(accessFile)
	if err != nil {
		return nil
	}
	var out []uint32
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if n, err := strconv.ParseUint(line, 10, 32); err == nil {
			out = append(out, uint32(n))
		}
	}
	return out
}
