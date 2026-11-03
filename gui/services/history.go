package services

import (
	"context"
	"errors"
	"sync"
)

// HistoryService owns the GUI-side bounded activity history. It is off by
// default; the daemon never stores history.
type HistoryService struct {
	mu      sync.Mutex
	enabled bool
}

func (s *HistoryService) Samples(context.Context, HistoryRange) ([]any, error) {
	return []any{}, nil
}

func (s *HistoryService) Summary(context.Context) (HistorySummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return HistorySummary{Enabled: s.enabled, IntervalS: 5, RetainMB: 50}, nil
}

func (s *HistoryService) SetEnabled(_ context.Context, enabled bool) error {
	s.mu.Lock()
	s.enabled = enabled
	s.mu.Unlock()
	return nil
}

func (s *HistoryService) Clear(context.Context) error { return nil }

func (s *HistoryService) Export(context.Context, string) (string, error) {
	return "", errors.New("history export is available after enabling history")
}
