package services

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubHistoryStore redirects the store to a temp dir and restores every
// package-level seam. Tests using it must not run in parallel.
func stubHistoryStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldDir, oldDialog := historyDir, saveFileDialog
	oldAge, oldBytes := historyMaxAge, historyMaxBytes
	historyDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() {
		historyDir, saveFileDialog = oldDir, oldDialog
		historyMaxAge, historyMaxBytes = oldAge, oldBytes
	})
	return dir
}

func sampleAt(at time.Time, rx float64) HistorySample {
	return HistorySample{
		T:          at.UnixMilli(),
		RxPerSec:   rx,
		TxPerSec:   rx / 2,
		PeersUp:    1,
		PeersTotal: 2,
	}
}

func TestHistoryDisabledByDefault(t *testing.T) {
	dir := stubHistoryStore(t)
	ctx := context.Background()
	svc := &HistoryService{}

	summary, err := svc.Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.Enabled {
		t.Fatal("history is enabled by default, want opt-in")
	}
	if err := svc.Record(ctx, sampleAt(time.Now(), 10)); err != nil {
		t.Fatalf("Record while disabled: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, historyFileName)); !os.IsNotExist(err) {
		t.Fatalf("Record while disabled created %s", historyFileName)
	}
	samples, err := svc.Samples(ctx, HistoryRange{})
	if err != nil {
		t.Fatalf("Samples: %v", err)
	}
	if len(samples) != 0 {
		t.Fatalf("samples = %d, want 0", len(samples))
	}
}

func TestHistoryEnablePersistsAcrossInstances(t *testing.T) {
	dir := stubHistoryStore(t)
	ctx := context.Background()
	if err := (&HistoryService{}).SetEnabled(ctx, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, historySettings)); err != nil {
		t.Fatalf("settings file: %v", err)
	}
	summary, err := (&HistoryService{}).Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if !summary.Enabled {
		t.Fatal("enabled setting did not persist to a fresh instance")
	}
	if summary.IntervalS != 5 || summary.RetainMB != 50 {
		t.Fatalf("summary = %+v, want 5 s / 50 MB", summary)
	}
}

func TestHistoryAppendReadAndFilter(t *testing.T) {
	stubHistoryStore(t)
	ctx := context.Background()
	svc := &HistoryService{}
	if err := svc.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	want := []HistorySample{
		sampleAt(now.Add(-2*time.Hour), 1),
		sampleAt(now.Add(-1*time.Hour), 2),
		sampleAt(now, 3),
	}
	for _, sample := range want {
		if err := svc.Record(ctx, sample); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	all, err := svc.Samples(ctx, HistoryRange{})
	if err != nil {
		t.Fatalf("Samples: %v", err)
	}
	if len(all) != len(want) {
		t.Fatalf("samples = %d, want %d", len(all), len(want))
	}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("sample %d = %+v, want %+v", i, all[i], want[i])
		}
	}
	recent, err := svc.Samples(ctx, HistoryRange{Minutes: 90})
	if err != nil {
		t.Fatalf("Samples filtered: %v", err)
	}
	if len(recent) != 2 || recent[0].RxPerSec != 2 || recent[1].RxPerSec != 3 {
		t.Fatalf("filtered samples = %+v, want the last two", recent)
	}
	summary, err := svc.Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.StoredPoints != uint64(len(want)) || summary.StoredBytes == 0 {
		t.Fatalf("summary = %+v, want %d points and bytes", summary, len(want))
	}
}

func TestHistoryRetentionByAgeAndSize(t *testing.T) {
	dir := stubHistoryStore(t)
	ctx := context.Background()
	svc := &HistoryService{}
	if err := svc.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	historyMaxAge = time.Hour
	if err := svc.Record(ctx, sampleAt(time.Now().Add(-2*time.Hour), 1)); err != nil {
		t.Fatal(err)
	}
	samples, err := svc.Samples(ctx, HistoryRange{})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 0 {
		t.Fatalf("stale samples = %+v, want none", samples)
	}

	historyMaxAge = 24 * time.Hour
	historyMaxBytes = 200
	var last HistorySample
	for i := range 10 {
		last = sampleAt(time.Now().Add(time.Duration(i)*time.Second), float64(i))
		if err := svc.Record(ctx, last); err != nil {
			t.Fatal(err)
		}
	}
	samples, err = svc.Samples(ctx, HistoryRange{})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) == 0 || len(samples) > 3 {
		t.Fatalf("samples after size trim = %d, want a bounded tail", len(samples))
	}
	if samples[len(samples)-1] != last {
		t.Fatalf("newest sample dropped: %+v, want %+v", samples[len(samples)-1], last)
	}
	st, err := os.Stat(filepath.Join(dir, historyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > historyMaxBytes {
		t.Fatalf("history size = %d, want <= %d", st.Size(), historyMaxBytes)
	}
}

func TestHistoryClearKeepsEnabled(t *testing.T) {
	dir := stubHistoryStore(t)
	ctx := context.Background()
	svc := &HistoryService{}
	if err := svc.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Record(ctx, sampleAt(time.Now(), 5)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, historyFileName)); !os.IsNotExist(err) {
		t.Fatal("Clear left the history file")
	}
	samples, err := svc.Samples(ctx, HistoryRange{})
	if err != nil || len(samples) != 0 {
		t.Fatalf("Samples after Clear = %+v, %v", samples, err)
	}
	summary, err := svc.Summary(ctx)
	if err != nil || !summary.Enabled {
		t.Fatalf("Summary after Clear = %+v, %v; want still enabled", summary, err)
	}
}

func TestHistoryExportJSONAndCSV(t *testing.T) {
	dir := stubHistoryStore(t)
	ctx := context.Background()
	svc := &HistoryService{}
	if err := svc.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := svc.Record(ctx, sampleAt(now.Add(-time.Minute), 4)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Record(ctx, sampleAt(now, 8)); err != nil {
		t.Fatal(err)
	}

	var suggested string
	saveFileDialog = func(name, filterName, pattern string) (string, error) {
		suggested = name
		if filterName != "JSON" || pattern != "*.json" {
			t.Errorf("json filter = %q/%q", filterName, pattern)
		}
		return filepath.Join(dir, "out.json"), nil
	}
	path, err := svc.Export(ctx, "json")
	if err != nil {
		t.Fatalf("Export json: %v", err)
	}
	if path != filepath.Join(dir, "out.json") || !strings.HasSuffix(suggested, ".json") {
		t.Fatalf("export path = %q, suggested = %q", path, suggested)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []HistorySample
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json export: %v", err)
	}
	if len(got) != 2 || got[0].RxPerSec != 4 || got[1].RxPerSec != 8 {
		t.Fatalf("json export samples = %+v", got)
	}

	saveFileDialog = func(name, filterName, pattern string) (string, error) {
		if filterName != "CSV" || pattern != "*.csv" {
			t.Errorf("csv filter = %q/%q", filterName, pattern)
		}
		return filepath.Join(dir, "out.csv"), nil
	}
	path, err = svc.Export(ctx, "csv")
	if err != nil {
		t.Fatalf("Export csv: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("csv export: %v", err)
	}
	if len(rows) != 3 || strings.Join(rows[0], ",") != "t,rx_per_sec,tx_per_sec,peers_up,peers_total" {
		t.Fatalf("csv rows = %+v", rows)
	}
	if rows[1][1] != "4" || rows[2][1] != "8" {
		t.Fatalf("csv rates = %q, %q", rows[1][1], rows[2][1])
	}
}

func TestHistoryExportCancelledAndInvalidFormat(t *testing.T) {
	stubHistoryStore(t)
	ctx := context.Background()
	svc := &HistoryService{}
	if err := svc.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	called := false
	saveFileDialog = func(string, string, string) (string, error) {
		called = true
		return "", nil
	}
	if _, err := svc.Export(ctx, "json"); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancelled export error = %v", err)
	}
	if !called {
		t.Fatal("export skipped the dialog")
	}

	called = false
	if _, err := svc.Export(ctx, "yaml"); err == nil {
		t.Fatal("unsupported format accepted")
	}
	if called {
		t.Fatal("invalid format opened the dialog")
	}
}

func TestHistorySamplesHideRowsPastRetention(t *testing.T) {
	dir := stubHistoryStore(t)
	historyMaxAge = time.Hour
	ctx := context.Background()
	now := time.Now()
	stale := sampleAt(now.Add(-2*time.Hour), 1)
	fresh := sampleAt(now.Add(-time.Minute), 2)

	// Write directly: Record would trim the stale row as part of its append,
	// and the point of the test is that Samples hides it without one.
	var buf bytes.Buffer
	for _, sample := range []HistorySample{stale, fresh} {
		raw, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(raw, '\n'))
	}
	if err := os.WriteFile(filepath.Join(dir, historyFileName), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	samples, err := (&HistoryService{}).Samples(ctx, HistoryRange{})
	if err != nil {
		t.Fatalf("Samples: %v", err)
	}
	if len(samples) != 1 || samples[0] != fresh {
		t.Fatalf("samples = %+v, want only the fresh row", samples)
	}

	// A window wider than retention must not resurrect the stale row.
	samples, err = (&HistoryService{}).Samples(ctx, HistoryRange{Minutes: 300})
	if err != nil {
		t.Fatalf("Samples wide window: %v", err)
	}
	if len(samples) != 1 || samples[0] != fresh {
		t.Fatalf("wide-window samples = %+v, want only the fresh row", samples)
	}
}

func TestHistoryExportTightensExistingFileMode(t *testing.T) {
	dir := stubHistoryStore(t)
	ctx := context.Background()
	svc := &HistoryService{}
	if err := svc.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Record(ctx, sampleAt(time.Now(), 3)); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(out, []byte("stale history"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(out, 0o644); err != nil {
		t.Fatal(err)
	}
	saveFileDialog = func(string, string, string) (string, error) { return out, nil }

	if _, err := svc.Export(ctx, "json"); err != nil {
		t.Fatalf("Export: %v", err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("overwritten export mode = %#o, want 0600", perm)
	}
}
