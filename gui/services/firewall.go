package services

import (
	"context"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// FirewallService reports and repairs the managed firewall rules.
type FirewallService struct{}

func (s *FirewallService) Status(ctx context.Context) (api.FirewallStatus, error) {
	c, err := ControlClient()
	if err != nil {
		return api.FirewallStatus{}, err
	}
	var out api.FirewallStatus
	err = c.do(ctx, "GET", "/api/v1/firewall", nil, &out)
	return out, err
}

func (s *FirewallService) Repair(ctx context.Context) (api.FirewallStatus, error) {
	c, err := ControlClient()
	if err != nil {
		return api.FirewallStatus{}, err
	}
	var out api.FirewallStatus
	err = c.do(ctx, "POST", "/api/v1/firewall/repair", nil, &out)
	return out, err
}
