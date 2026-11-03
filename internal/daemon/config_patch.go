package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"unicode"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
)

// Restart matrix: name and the STUN toggle apply live; listen_port, mtu,
// interface, public_addr, mdns, and stun_server need a service restart.

// PatchConfig implements api.GUI. It merges the allowlisted fields under the
// same lock as peer persistence, validates the merged config before writing
// anything (preflight; an invalid merge leaves the old config untouched,
// which is the rollback), and persists it.
func (d *Daemon) PatchConfig(ctx context.Context, p api.ConfigPatch) (api.ConfigPatchResult, error) {
	var (
		applied         []string
		restartRequired bool
	)
	fail := func(field, message string) (api.ConfigPatchResult, error) {
		return api.ConfigPatchResult{}, &api.StatusError{
			Status:      http.StatusBadRequest,
			Code:        api.ErrCodeValidationFailed,
			Message:     message,
			Remediation: "check the value and retry",
			Details:     map[string]string{"field": field},
		}
	}

	d.persistMu.Lock()
	d.cfgMu.Lock()
	cfg := d.cfg

	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if err := conf.ValidateName(name); err != nil {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("name", err.Error())
		}
		cfg.Node.Name = name
		applied = append(applied, "name")
	}
	if p.ListenPort != nil {
		if *p.ListenPort < 1 {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("listen_port", "Port must be 1-65535")
		}
		cfg.Node.ListenPort = *p.ListenPort
		applied = append(applied, "listen_port")
		restartRequired = true
	}
	if p.MTU != nil {
		if *p.MTU < conf.MinMTU || *p.MTU > conf.MaxMTU {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("mtu", fmt.Sprintf("mtu must be %d-%d", conf.MinMTU, conf.MaxMTU))
		}
		cfg.Node.MTU = *p.MTU
		applied = append(applied, "mtu")
		restartRequired = true
	}
	if p.Interface != nil {
		if err := validateInterfaceName(*p.Interface); err != nil {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("interface", err.Error())
		}
		cfg.Node.Interface = *p.Interface
		applied = append(applied, "interface")
		restartRequired = true
	}
	if p.PublicAddr != nil {
		addr, err := netip.ParseAddrPort(*p.PublicAddr)
		if err != nil || addr.Addr().IsUnspecified() {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("public_addr", "public_addr must be an IP:port")
		}
		cfg.Node.PublicAddr = addr
		applied = append(applied, "public_addr")
		restartRequired = true
	}
	if p.MDNS != nil {
		cfg.Discovery.MDNS = *p.MDNS
		applied = append(applied, "mdns")
		restartRequired = true
	}
	if p.STUN != nil {
		cfg.Discovery.STUN = *p.STUN
		applied = append(applied, "stun") // live: WAN refresh re-reads the config
	}
	if p.STUNServer != nil {
		if *p.STUNServer != "" {
			if _, err := netip.ParseAddrPort(*p.STUNServer); err != nil {
				d.cfgMu.Unlock()
				d.persistMu.Unlock()
				return fail("stun_server", "stun_server must be an IP:port")
			}
		}
		cfg.Discovery.STUNServer = *p.STUNServer
		applied = append(applied, "stun_server")
		restartRequired = true
	}

	// Preflight: validate the whole merged config before persisting.
	if err := conf.Validate(cfg); err != nil {
		d.cfgMu.Unlock()
		d.persistMu.Unlock()
		return fail("", "merged configuration is invalid: "+err.Error())
	}
	if len(applied) == 0 {
		d.cfgMu.Unlock()
		d.persistMu.Unlock()
		return api.ConfigPatchResult{}, nil
	}
	d.cfg = cfg
	d.cfgMu.Unlock()

	if p.Name != nil {
		d.ctl.SetLocalName(strings.TrimSpace(*p.Name))
	}
	d.persistLocked(cfg)
	d.persistMu.Unlock()
	if p.Name != nil {
		d.emit(api.Event{Type: "name_changed"})
	}
	return api.ConfigPatchResult{RestartRequired: restartRequired, Applied: applied}, nil
}

// validateInterfaceName mirrors the conf file-level rule for node.interface.
func validateInterfaceName(name string) error {
	if name == "" || len(name) > 15 {
		return errors.New("interface must be 1-15 characters")
	}
	if strings.HasPrefix(name, "-") {
		return errors.New("interface must not start with '-'")
	}
	if name == "." || name == ".." {
		return errors.New("interface is reserved")
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.'
		if !ok {
			return fmt.Errorf("interface contains invalid character %q", r)
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("interface contains a control character")
		}
	}
	return nil
}
