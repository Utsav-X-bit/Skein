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
// A patch that sets a field to the value it already holds changes nothing:
// it is not reported as applied and does not ask for a restart.

// PatchConfig implements api.GUI. It merges the allowlisted fields under the
// same lock as peer persistence, validates the merged config before writing
// anything (preflight; an invalid merge leaves the old config untouched,
// which is the rollback), and persists it. The live config and the advertised
// name move only after the write succeeds, so a failed save cannot leave the
// daemon running on a value the disk does not have.
func (d *Daemon) PatchConfig(ctx context.Context, p api.ConfigPatch) (api.ConfigPatchResult, error) {
	var (
		applied     []string
		restart     bool
		nameChanged bool
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
		// An empty name means "the default": conf.Parse leaves the key out
		// and New fills the host name at startup, so resolve it here too
		// instead of advertising and persisting a nameless node.
		if name == "" {
			name = conf.DefaultNodeName()
		}
		if err := conf.ValidateName(name); err != nil {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("name", err.Error())
		}
		if name != cfg.Node.Name {
			cfg.Node.Name = name
			applied = append(applied, "name")
			nameChanged = true
		}
	}
	if p.ListenPort != nil {
		if *p.ListenPort == 0 {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("listen_port", "Port must be 1-65535")
		}
		if *p.ListenPort != cfg.Node.ListenPort {
			cfg.Node.ListenPort = *p.ListenPort
			applied = append(applied, "listen_port")
			restart = true
		}
	}
	if p.MTU != nil {
		if *p.MTU < conf.MinMTU || *p.MTU > conf.MaxMTU {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("mtu", fmt.Sprintf("mtu must be %d-%d", conf.MinMTU, conf.MaxMTU))
		}
		if *p.MTU != cfg.Node.MTU {
			cfg.Node.MTU = *p.MTU
			applied = append(applied, "mtu")
			restart = true
		}
	}
	if p.Interface != nil {
		if err := validateInterfaceName(*p.Interface); err != nil {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("interface", err.Error())
		}
		if *p.Interface != cfg.Node.Interface {
			cfg.Node.Interface = *p.Interface
			applied = append(applied, "interface")
			restart = true
		}
	}
	if p.PublicAddr != nil {
		// Empty clears the optional address (the field is only written when
		// it is set); otherwise the value must satisfy the same rule
		// conf.Validate applies to the parsed file.
		text := strings.TrimSpace(*p.PublicAddr)
		addr := netip.AddrPort{}
		if text != "" {
			parsed, err := netip.ParseAddrPort(text)
			if err != nil {
				d.cfgMu.Unlock()
				d.persistMu.Unlock()
				return fail("public_addr", "public_addr must be an ip:port, or empty to clear it")
			}
			addr = netip.AddrPortFrom(parsed.Addr().Unmap(), parsed.Port())
			if addr.Port() == 0 || addr.Addr().IsUnspecified() || addr.Addr().IsMulticast() {
				d.cfgMu.Unlock()
				d.persistMu.Unlock()
				return fail("public_addr", fmt.Sprintf("public_addr must be a unicast ip:port, got %s", addr))
			}
			if cfg.Node.OverlayIPv4Prefix.Contains(addr.Addr()) || cfg.Node.OverlayIPv6Prefix.Contains(addr.Addr()) {
				d.cfgMu.Unlock()
				d.persistMu.Unlock()
				return fail("public_addr", fmt.Sprintf("public_addr %s must not be inside the overlay prefixes", addr))
			}
		}
		if addr != cfg.Node.PublicAddr {
			cfg.Node.PublicAddr = addr
			applied = append(applied, "public_addr")
			restart = true
		}
	}
	if p.MDNS != nil {
		if *p.MDNS != cfg.Discovery.MDNS {
			cfg.Discovery.MDNS = *p.MDNS
			applied = append(applied, "mdns")
			restart = true
		}
	}
	if p.STUN != nil {
		if *p.STUN != cfg.Discovery.STUN {
			cfg.Discovery.STUN = *p.STUN
			applied = append(applied, "stun") // live: WAN refresh re-reads the config
		}
	}
	if p.STUNServer != nil {
		// conf.Parse treats an absent stun_server as the default, so an
		// empty value here restores it instead of writing an unusable one.
		// The accepted syntax is host:port — hostnames are supported
		// (the default is one), so this uses the same rule as
		// conf.Validate rather than an IP-only parse.
		server := *p.STUNServer
		if server == "" {
			server = conf.DefaultSTUNServer
		} else if err := conf.ValidateSTUNServer(server); err != nil {
			d.cfgMu.Unlock()
			d.persistMu.Unlock()
			return fail("stun_server", err.Error())
		}
		if server != cfg.Discovery.STUNServer {
			cfg.Discovery.STUNServer = server
			applied = append(applied, "stun_server")
			restart = true
		}
	}

	// Nothing changed: report success without touching the disk or asking
	// for a restart, and without re-validating a config this patch did not
	// make worse.
	if len(applied) == 0 {
		d.cfgMu.Unlock()
		d.persistMu.Unlock()
		return api.ConfigPatchResult{}, nil
	}

	// Preflight: validate the whole merged config before persisting.
	if err := conf.Validate(cfg); err != nil {
		d.cfgMu.Unlock()
		d.persistMu.Unlock()
		return fail("", "merged configuration is invalid: "+err.Error())
	}
	d.cfgMu.Unlock()

	// Persist before publishing: persistLocked updates d.cfg only after a
	// successful write, and a failure leaves both the live config and the
	// advertised name untouched.
	if err := d.persistLocked(cfg); err != nil {
		d.persistMu.Unlock()
		return api.ConfigPatchResult{}, err
	}

	if nameChanged {
		d.ctl.SetLocalName(cfg.Node.Name)
	}
	d.persistMu.Unlock()
	if nameChanged {
		d.emit(api.Event{Type: "name_changed"})
	}
	return api.ConfigPatchResult{RestartRequired: restart, Applied: applied}, nil
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
