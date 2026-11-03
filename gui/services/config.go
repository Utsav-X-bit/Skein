package services

import (
	"context"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// ConfigService reads and patches the effective configuration.
type ConfigService struct{}

func (s *ConfigService) Get(ctx context.Context) (api.ConfigView, error) {
	c, err := ControlClient()
	if err != nil {
		return api.ConfigView{}, err
	}
	var out api.ConfigView
	err = c.do(ctx, "GET", "/api/v1/config", nil, &out)
	return out, err
}

func (s *ConfigService) Patch(ctx context.Context, patch api.ConfigPatch) (api.ConfigPatchResult, error) {
	c, err := ControlClient()
	if err != nil {
		return api.ConfigPatchResult{}, err
	}
	var out api.ConfigPatchResult
	err = c.do(ctx, "PATCH", "/api/v1/config", patch, &out)
	return out, err
}

func (s *ConfigService) SetName(ctx context.Context, name string) error {
	c, err := ControlClient()
	if err != nil {
		return err
	}
	return c.do(ctx, "PATCH", "/api/v1/config", api.ConfigPatch{Name: &name}, nil)
}
