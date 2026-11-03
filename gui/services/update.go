package services

import "context"

// UpdateService checks for GUI updates.
type UpdateService struct{}

// Check reports an available update, if any.
func (s *UpdateService) Check(ctx context.Context) (UpdateInfo, error) {
	return UpdateInfo{}, ErrDaemonNotConnected
}
