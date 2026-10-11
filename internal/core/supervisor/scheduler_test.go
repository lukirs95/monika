package supervisor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestOffset(t *testing.T) {
	id := uuid.MustParse("01a12b7d-a1fa-73f7-9940-a3101d57e5f1")
	first := offset(id, 0, time.Second)
	if again := offset(uuid.MustParse(id.String()), 0, time.Second); again != first {
		t.Fatal("offset not deterministic")
	}
	if offset(id, 0, time.Second) == offset(id, 1, time.Second) {
		t.Error("two tasks of one source share their offset")
	}
	buckets := map[time.Duration]bool{}
	for range 100 {
		o := offset(uuid.New(), 0, time.Second)
		if o < 0 || o >= time.Second {
			t.Fatalf("offset %v outside the interval", o)
		}
		buckets[o/(100*time.Millisecond)] = true
	}
	if len(buckets) < 8 {
		t.Errorf("offsets of 100 sources fall into %d of 10 buckets", len(buckets))
	}
}

func TestNextTick(t *testing.T) {
	base := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		now  time.Duration
		want time.Duration
	}{
		{0, 700 * time.Millisecond},
		{500 * time.Millisecond, 700 * time.Millisecond},
		{700 * time.Millisecond, 2700 * time.Millisecond}, // genau auf dem Takt: der nächste
		{1900 * time.Millisecond, 2700 * time.Millisecond},
	}
	for _, tt := range tests {
		if got := nextTick(base.Add(tt.now), 2*time.Second, 700*time.Millisecond); !got.Equal(base.Add(tt.want)) {
			t.Errorf("nextTick(%v) = %v, want %v", tt.now, got.Sub(base), tt.want)
		}
	}
}

// every startet eine Aufgabe und liefert eine Funktion, die sie beendet.
func every(s *Scheduler, interval time.Duration, fn func(context.Context) error, onPanic func(error), log *slog.Logger) func() {
	ctx, cancel := context.WithCancel(context.Background())
	s.For(uuid.New(), log, onPanic).Every(ctx, interval, fn)
	return cancel
}

func TestEveryCallsAndStops(t *testing.T) {
	var calls atomic.Int32
	stop := every(NewScheduler(4), 10*time.Millisecond, func(context.Context) error { calls.Add(1); return nil }, nil, discard())
	time.Sleep(100 * time.Millisecond)
	stop()
	n := calls.Load()
	if n < 4 {
		t.Errorf("calls = %d in 100 ms at 10 ms, want at least 4", n)
	}
	time.Sleep(40 * time.Millisecond)
	if calls.Load() > n+1 {
		t.Errorf("calls after stop: %d → %d", n, calls.Load())
	}
}

// overlap misst, wie viele Aufrufe höchstens gleichzeitig liefen.
type overlap struct {
	now, max atomic.Int32
}

func (o *overlap) fn(d time.Duration) func(context.Context) error {
	return func(context.Context) error {
		n := o.now.Add(1)
		for m := o.max.Load(); n > m && !o.max.CompareAndSwap(m, n); m = o.max.Load() {
		}
		time.Sleep(d)
		o.now.Add(-1)
		return nil
	}
}

func TestEveryNoOverlap(t *testing.T) {
	var o overlap
	stop := every(NewScheduler(4), 5*time.Millisecond, o.fn(30*time.Millisecond), nil, discard())
	time.Sleep(100 * time.Millisecond)
	stop()
	if o.max.Load() != 1 {
		t.Errorf("max concurrent calls of one task = %d", o.max.Load())
	}
}

func TestGlobalLimit(t *testing.T) {
	s := NewScheduler(1)
	var o overlap
	stop1 := every(s, 5*time.Millisecond, o.fn(10*time.Millisecond), nil, discard())
	stop2 := every(s, 5*time.Millisecond, o.fn(10*time.Millisecond), nil, discard())
	time.Sleep(100 * time.Millisecond)
	stop1()
	stop2()
	if o.max.Load() != 1 {
		t.Errorf("max concurrent calls with limit 1 = %d", o.max.Load())
	}
}

func TestEveryTimeout(t *testing.T) {
	errs := make(chan error, 10)
	stop := every(NewScheduler(4), 20*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		errs <- ctx.Err()
		return ctx.Err()
	}, nil, discard())
	defer stop()
	if err := <-errs; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ctx of a call ended with %v, want deadline", err)
	}
}

func TestEveryPanic(t *testing.T) {
	panics := make(chan error, 10)
	var calls atomic.Int32
	stop := every(NewScheduler(4), 10*time.Millisecond, func(context.Context) error {
		calls.Add(1)
		panic("boom")
	}, func(err error) { panics <- err }, discard())
	defer stop()
	if err := <-panics; !strings.Contains(err.Error(), "boom") {
		t.Errorf("onPanic got %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if calls.Load() < 2 {
		t.Error("task stopped after a panic")
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestEveryHangLoggedOnce(t *testing.T) {
	var logs syncBuffer
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	stop := every(NewScheduler(4), 10*time.Millisecond, func(context.Context) error {
		calls.Add(1)
		<-release // hält den Timeout nicht ein
		return nil
	}, nil, slog.New(slog.NewTextHandler(&logs, nil)))
	time.Sleep(100 * time.Millisecond)
	stop()
	if got := strings.Count(logs.String(), "scheduled task hangs"); got != 1 {
		t.Errorf("hang logged %d times", got)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want no new call while one hangs", calls.Load())
	}
}
