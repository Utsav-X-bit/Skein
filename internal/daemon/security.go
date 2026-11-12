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

// grantedUIDs reads the persisted granted-uid list from the IPC layer
// (controlAccessFile: /etc/skein/access on Unix, the app-private
// access file on Android). Lines are "uid" or "uid:role" (Step 1);
// roles are stripped here because this view lists granted identities,
// not their roles.
func grantedUIDs() []uint32 {
	if runtime.GOOS == "windows" {
		return nil // Windows grants are SIDs, persisted by the pipe layer
	}
	raw, err := os.ReadFile(controlAccessFile)
	if err != nil {
		return nil
	}
	grants, err := api.ParseAccessGrants(strings.NewReader(string(raw)))
	if err != nil {
		return nil
	}
	var out []uint32
	for id := range grants {
		uidStr, ok := strings.CutPrefix(id, "uid:")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(uidStr, 10, 32); err == nil {
			out = append(out, uint32(n))
		}
	}
	return out
}
