package retention

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type fakeStore struct {
	left  map[string]int // pro Art noch zu löschende Zeilen
	calls map[string]int
	err   error
}

func (f *fakeStore) take(kind string, limit int) (int, error) {
	f.calls[kind]++
	if f.err != nil {
		return 0, f.err
	}
	n := min(f.left[kind], limit)
	f.left[kind] -= n
	return n, nil
}

func (f *fakeStore) PurgeFindings(_ context.Context, _ time.Time, limit int) (int, error) {
	return f.take("findings", limit)
}
func (f *fakeStore) PurgeRuns(_ context.Context, _ time.Time, limit int) (int, error) {
	return f.take("runs", limit)
}
func (f *fakeStore) PurgeAudit(_ context.Context, _ time.Time, limit int) (int, error) {
	return f.take("audit", limit)
}
func (f *fakeStore) PurgeSessions(context.Context, time.Time) (int, error) {
	return f.take("sessions", 1<<30)
}

func TestPurgeInBatches(t *testing.T) {
	store := &fakeStore{left: map[string]int{"findings": 2500, "runs": 10, "audit": 5, "sessions": 3}, calls: map[string]int{}}
	c := New(Config{Store: store, Findings: time.Hour, Runs: time.Hour, Audit: 0, Log: slog.New(slog.DiscardHandler)})
	c.pause = 0
	c.purge(t.Context())
	want := map[string]int{"findings": 3, "runs": 1, "audit": 0, "sessions": 1}
	for k, n := range want {
		if store.calls[k] != n {
			t.Errorf("%s: %d calls, want %d", k, store.calls[k], n)
		}
	}
	if store.left["findings"] != 0 || store.left["audit"] != 5 {
		t.Errorf("left = %v; audit (0 = never) must stay", store.left)
	}
}

func TestPurgeStopsOnError(t *testing.T) {
	store := &fakeStore{left: map[string]int{"findings": 5000}, calls: map[string]int{}, err: errors.New("db down")}
	c := New(Config{Store: store, Findings: time.Hour, Log: slog.New(slog.DiscardHandler)})
	c.purge(t.Context())
	if store.calls["findings"] != 1 {
		t.Errorf("calls after an error = %d", store.calls["findings"])
	}
}

func TestNextRun(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone data")
	}
	tests := []struct {
		now, want time.Time
	}{
		{time.Date(2026, 10, 11, 1, 0, 0, 0, berlin), time.Date(2026, 10, 11, 3, 30, 0, 0, berlin)},
		{time.Date(2026, 10, 11, 3, 30, 0, 0, berlin), time.Date(2026, 10, 12, 3, 30, 0, 0, berlin)},
		{time.Date(2026, 10, 24, 12, 0, 0, 0, berlin), time.Date(2026, 10, 25, 3, 30, 0, 0, berlin)}, // Umstellung auf Winterzeit
	}
	for _, tt := range tests {
		if got := nextRun(tt.now); !got.Equal(tt.want) {
			t.Errorf("nextRun(%v) = %v, want %v", tt.now, got, tt.want)
		}
	}
}
