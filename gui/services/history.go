package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// History is GUI-local and opt-in: the daemon never stores samples, and the
// store lives in the user's config directory so a system-wide install never
// writes to a privileged location.
const (
	historyIntervalS = 5
	historyRetainMB  = 50
	historyFileName  = "history.jsonl"
	historySettings  = "settings.json"
)

// historyMaxAge and historyMaxBytes are variables so tests can shrink the
// retention window without writing 50 MB.
var (
	historyMaxAge   = 24 * time.Hour
	historyMaxBytes = int64(historyRetainMB) << 20

	// historyDir resolves the store directory. Tests replace it with a
	// t.TempDir so no test depends on os.UserConfigDir.
	historyDir = func() (string, error) {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("history: config directory: %w", err)
		}
		return filepath.Join(base, "skein-desktop", "history"), nil
	}

	// saveFileDialog prompts for a save path. Tests replace it to simulate
	// cancel and success without a Wails application. A server-mode build
	// has no application, so it reports a clear error instead of silently
	// returning an empty path.
	saveFileDialog = func(suggested, filterName, pattern string) (string, error) {
		app := application.Get()
		if app == nil {
			return "", errors.New("file dialogs need the desktop application")
		}
		return app.Dialog.SaveFile().
			SetFilename(suggested).
			AddFilter(filterName, pattern).
			PromptForSingleSelection()
	}
)

// HistorySample is one recorded aggregate mesh sample. Rates are computed by
// the caller over its own window; the store never infers them.
type HistorySample struct {
	T          int64   `json:"t"`
	RxPerSec   float64 `json:"rx_per_sec"`
	TxPerSec   float64 `json:"tx_per_sec"`
	PeersUp    int     `json:"peers_up"`
	PeersTotal int     `json:"peers_total"`
}

type historySettingsFile struct {
	Enabled bool `json:"enabled"`
}

// HistoryService owns the GUI-side bounded activity history. It is off by
// default; the daemon never stores history.
type HistoryService struct {
	mu sync.Mutex
}

// Record appends one sample when history is enabled. It is a no-op while
// disabled, so callers need no enable check of their own.
func (s *HistoryService) Record(ctx context.Context, sample HistorySample) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := historyDir()
	if err != nil {
		return err
	}
	settings, err := readHistorySettings(dir)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	if sample.T == 0 {
		sample.T = time.Now().UnixMilli()
	}
	raw, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("history: create store: %w", err)
	}
	path := filepath.Join(dir, historyFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("history: append: %w", err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("history: append: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("history: append: %w", err)
	}
	return trimHistory(path, time.Now())
}

// Samples returns recorded samples, optionally limited to the last N minutes.
// Rows past the retention window are dropped even when no append has trimmed
// the file yet, so a long-idle app never reports samples it would discard.
func (s *HistoryService) Samples(ctx context.Context, r HistoryRange) ([]HistorySample, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := historyDir()
	if err != nil {
		return nil, err
	}
	samples, err := readHistory(filepath.Join(dir, historyFileName))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cutoff := now.Add(-historyMaxAge).UnixMilli()
	if r.Minutes > 0 {
		if window := now.Add(-time.Duration(r.Minutes) * time.Minute).UnixMilli(); window > cutoff {
			cutoff = window
		}
	}
	out := samples[:0]
	for _, sample := range samples {
		if sample.T >= cutoff {
			out = append(out, sample)
		}
	}
	return out, nil
}

// Summary reports the store state without exposing sample contents.
func (s *HistoryService) Summary(ctx context.Context) (HistorySummary, error) {
	if err := ctx.Err(); err != nil {
		return HistorySummary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := historyDir()
	if err != nil {
		return HistorySummary{}, err
	}
	settings, err := readHistorySettings(dir)
	if err != nil {
		return HistorySummary{}, err
	}
	out := HistorySummary{
		Enabled:   settings.Enabled,
		IntervalS: historyIntervalS,
		RetainMB:  historyRetainMB,
	}
	size, points, err := historyStats(filepath.Join(dir, historyFileName))
	if err != nil {
		return HistorySummary{}, err
	}
	out.StoredBytes = size
	out.StoredPoints = points
	return out, nil
}

// SetEnabled persists the opt-in flag so it survives app restarts.
func (s *HistoryService) SetEnabled(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := historyDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("history: create store: %w", err)
	}
	raw, err := json.Marshal(historySettingsFile{Enabled: enabled})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, historySettings), raw, 0o600)
}

// Clear deletes recorded samples but keeps the opt-in setting.
func (s *HistoryService) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := historyDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, historyFileName))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("history: clear: %w", err)
	}
	return nil
}

// Export writes the recorded samples as JSON or CSV through a native save
// dialog and returns the chosen path.
func (s *HistoryService) Export(ctx context.Context, format string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "json" && format != "csv" {
		return "", fmt.Errorf("history: unsupported export format %q (want json or csv)", format)
	}

	s.mu.Lock()
	dir, err := historyDir()
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	samples, err := readHistory(filepath.Join(dir, historyFileName))
	s.mu.Unlock()
	if err != nil {
		return "", err
	}

	raw, err := encodeHistory(samples, format)
	if err != nil {
		return "", err
	}
	name := "skein-history-" + time.Now().Format("20060102-150405") + "." + format
	path, err := saveFileDialog(name, strings.ToUpper(format), "*."+format)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", errors.New("history export cancelled")
	}
	if err := writeFileAtomic(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("history export: %w", err)
	}
	return path, nil
}

func encodeHistory(samples []HistorySample, format string) ([]byte, error) {
	if format == "json" {
		raw, err := json.MarshalIndent(samples, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(raw, '\n'), nil
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"t", "rx_per_sec", "tx_per_sec", "peers_up", "peers_total"})
	for _, sample := range samples {
		_ = w.Write([]string{
			strconv.FormatInt(sample.T, 10),
			strconv.FormatFloat(sample.RxPerSec, 'g', -1, 64),
			strconv.FormatFloat(sample.TxPerSec, 'g', -1, 64),
			strconv.Itoa(sample.PeersUp),
			strconv.Itoa(sample.PeersTotal),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readHistorySettings(dir string) (historySettingsFile, error) {
	var out historySettingsFile
	raw, err := os.ReadFile(filepath.Join(dir, historySettings))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("history: settings: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return historySettingsFile{}, fmt.Errorf("history: settings: %w", err)
	}
	return out, nil
}

func readHistory(path string) ([]HistorySample, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []HistorySample{}, nil
		}
		return nil, fmt.Errorf("history: read: %w", err)
	}
	defer f.Close()
	out := make([]HistorySample, 0, 64)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var sample HistorySample
		if json.Unmarshal(sc.Bytes(), &sample) != nil {
			continue
		}
		out = append(out, sample)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("history: read: %w", err)
	}
	return out, nil
}

func historyStats(path string) (uint64, uint64, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("history: stat: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("history: stat: %w", err)
	}
	defer f.Close()
	var points uint64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) > 0 {
			points++
		}
	}
	if err := sc.Err(); err != nil {
		return 0, 0, fmt.Errorf("history: stat: %w", err)
	}
	return uint64(st.Size()), points, nil
}

// trimHistory drops samples older than the retention window and then the
// oldest lines until the file fits the size cap. Appends are chronological,
// so only the head can be stale; a full rewrite runs only when one of the
// two limits is actually crossed.
func trimHistory(path string, now time.Time) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("history: trim: %w", err)
	}
	overSize := st.Size() > historyMaxBytes
	staleHead := false
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		if sc.Scan() {
			var sample HistorySample
			if json.Unmarshal(sc.Bytes(), &sample) == nil && sample.T > 0 &&
				now.Sub(time.UnixMilli(sample.T)) > historyMaxAge {
				staleHead = true
			}
		}
		f.Close()
	}
	if !overSize && !staleHead {
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("history: trim: %w", err)
	}
	cutoff := now.Add(-historyMaxAge).UnixMilli()
	lines := make([][]byte, 0, 64)
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var sample HistorySample
		if json.Unmarshal(line, &sample) != nil || sample.T < cutoff {
			continue
		}
		lines = append(lines, line)
	}
	var total int64
	for _, line := range lines {
		total += int64(len(line)) + 1
	}
	start := 0
	for total > historyMaxBytes && start < len(lines)-1 {
		total -= int64(len(lines[start])) + 1
		start++
	}
	lines = lines[start:]
	out := make([]byte, 0, total)
	for _, line := range lines {
		out = append(out, line...)
		out = append(out, '\n')
	}
	if bytes.Equal(out, raw) {
		return nil
	}
	return writeFileAtomic(path, out, 0o600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
