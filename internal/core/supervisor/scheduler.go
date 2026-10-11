// Package supervisor startet und stoppt die Quellen im Auftrag der Schleife des Core, fängt
// Panics, erkennt hängende Treiber und führt Befehle aus; dazu gehört der Scheduler für
// abfragende Treiber (docs/contracts/services.md, sdk.md).
package supervisor

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

// Scheduler verteilt die periodische Arbeit abfragender Treiber über die Zeit und begrenzt die
// Zahl gleichzeitiger Aufrufe über alle Quellen.
type Scheduler struct {
	sem chan struct{}
}

// NewScheduler liefert einen Scheduler mit höchstens concurrency gleichzeitigen Aufrufen.
func NewScheduler(concurrency int) *Scheduler {
	return &Scheduler{sem: make(chan struct{}, max(concurrency, 1))}
}

// For liefert das Handle einer Quelle für SourceConfig.Scheduler. onPanic wird bei einer Panic
// in einer Aufgabe gerufen; der Supervisor behandelt sie wie eine Panic in Run.
func (s *Scheduler) For(sourceID uuid.UUID, log *slog.Logger, onPanic func(error)) sdk.Scheduler {
	return &handle{s: s, source: sourceID, log: log, onPanic: onPanic}
}

type handle struct {
	s       *Scheduler
	source  uuid.UUID
	log     *slog.Logger
	onPanic func(error)
	tasks   atomic.Uint64
}

// Every ruft fn im Abstand interval auf, mit festem Versatz, bis ctx endet; es kehrt sofort
// zurück.
func (h *handle) Every(ctx context.Context, interval time.Duration, fn func(ctx context.Context) error) {
	if interval <= 0 {
		h.log.Error("scheduled task ignored: interval must be positive", "interval", interval)
		return
	}
	n := h.tasks.Add(1) - 1
	t := &task{
		h: h, interval: interval, offset: offset(h.source, n, interval), fn: fn,
		log: h.log.With("task", n, "interval", interval),
	}
	go t.run(ctx)
}

// offset ist der feste Versatz einer Aufgabe in ihrem Intervall, aus Quelle und Nummer.
func offset(source uuid.UUID, n uint64, interval time.Duration) time.Duration {
	f := fnv.New64a()
	f.Write(source[:])
	f.Write(binary.BigEndian.AppendUint64(nil, n))
	return time.Duration(f.Sum64() % uint64(interval))
}

// nextTick ist der nächste Zeitpunkt nach now, der im Raster interval auf offset liegt.
func nextTick(now time.Time, interval, offset time.Duration) time.Time {
	t := now.Truncate(interval).Add(offset)
	if !t.After(now) {
		t = t.Add(interval)
	}
	return t
}

type task struct {
	h        *handle
	interval time.Duration
	offset   time.Duration
	fn       func(ctx context.Context) error
	log      *slog.Logger

	running atomic.Bool
}

func (t *task) run(ctx context.Context) {
	timer := time.NewTimer(time.Until(nextTick(time.Now(), t.interval, t.offset)))
	defer timer.Stop()
	var started time.Time
	hungLogged := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now()
		switch {
		case !t.running.Load():
			t.running.Store(true)
			started, hungLogged = now, false
			go t.call(ctx)
		case !hungLogged && now.Sub(started) > t.interval:
			// Der Aufruf hält seinen Timeout nicht ein; es entsteht nichts Neues für ihn.
			t.log.Error("scheduled task hangs", "since", started)
			hungLogged = true
		default:
			t.log.Debug("scheduled task skipped, previous call still running")
		}
		timer.Reset(time.Until(nextTick(now, t.interval, t.offset)))
	}
}

// call wartet auf die globale Obergrenze und ruft fn mit Timeout hinter einer recover-Grenze.
func (t *task) call(ctx context.Context) {
	defer t.running.Store(false)
	select {
	case t.h.s.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-t.h.s.sem }()
	cctx, cancel := context.WithTimeout(ctx, t.interval)
	defer cancel()
	if err := t.guard(cctx); err != nil && ctx.Err() == nil {
		t.log.Warn("scheduled task failed", "err", err)
	}
}

func (t *task) guard(ctx context.Context) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic in scheduled task: %v", p)
			t.log.Error("scheduled task panicked", "panic", p)
			if t.h.onPanic != nil {
				t.h.onPanic(err)
			}
			err = nil
		}
	}()
	return t.fn(ctx)
}

var _ sdk.Scheduler = (*handle)(nil)
