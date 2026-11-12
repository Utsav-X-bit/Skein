package daemon

import (
	"context"
	"net/http"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/firewall"
)

// firewallStatus returns the firewall.Config for the live node.
func (d *Daemon) firewallStatus() firewall.Config {
	cfg := d.config()
	return firewall.Config{Interface: cfg.Node.Interface, Port: cfg.Node.ListenPort}
}

// toAPI converts the firewall package status to the wire view.
func firewallToAPI(st firewall.Status) api.FirewallStatus {
	out := api.FirewallStatus{
		Manager:     st.Manager,
		State:       st.State,
		LastCheckMS: st.LastCheckMS,
		LastError:   st.LastError,
		Note:        st.Note,
		Rules:       []api.FirewallRule{},
	}
	for _, r := range st.Rules {
		out.Rules = append(out.Rules, api.FirewallRule{Name: r.Name, Direction: r.Direction, Action: r.Action, Protocol: r.Protocol, Port: r.Port, Interface: r.Interface})
	}
	return out
}

// Firewall implements api.GUI: a best-effort inspection of the manager and
// the rules Skein manages. A missing manager is a state, not an error.
func (d *Daemon) Firewall() api.FirewallStatus {
	st, err := firewall.ReadStatus(d.firewallStatus())
	if err != nil {
		st.LastError = err.Error()
	}
	if st.LastCheckMS == 0 {
		st.LastCheckMS = time.Now().UnixMilli()
	}
	return firewallToAPI(st)
}

// RepairFirewall implements api.GUI. It re-applies the managed rules
// transactionally through the firewall package (add-before-remove, unique
// rule specs, rollback on failure) and returns the refreshed status.
func (d *Daemon) RepairFirewall(ctx context.Context) (api.FirewallStatus, error) {
	st, err := firewall.Repair(d.firewallStatus())
	if err != nil {
		return firewallToAPI(st), &api.StatusError{
			Status:      http.StatusBadGateway,
			Code:        api.ErrCodeInternal,
			Message:     err.Error(),
			Remediation: "check the firewall manager and retry",
		}
	}
	return firewallToAPI(st), nil
}
