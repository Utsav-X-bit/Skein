package services

import (
	"context"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// NetworkService reports the mesh: status, routes, activity.
type NetworkService struct{}

func (s *NetworkService) Status(ctx context.Context) (api.Status, error) {
	c, err := ControlClient()
	if err != nil {
		return api.Status{}, err
	}
	var out api.Status
	err = c.do(ctx, "GET", "/api/v1/status", nil, &out)
	return out, err
}

func (s *NetworkService) Routes(ctx context.Context) ([]api.Route, error) {
	c, err := ControlClient()
	if err != nil {
		return nil, err
	}
	var out []api.Route
	err = c.do(ctx, "GET", "/api/v1/routes", nil, &out)
	return out, err
}

func (s *NetworkService) Activities(ctx context.Context) ([]api.Activity, error) {
	c, err := ControlClient()
	if err != nil {
		return nil, err
	}
	var out []api.Activity
	err = c.do(ctx, "GET", "/api/v1/activities", nil, &out)
	return out, err
}
