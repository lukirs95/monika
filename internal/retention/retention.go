// Package retention löscht alten Verlauf: beendete Befunde, abgeschlossene Läufe, Audit, alte
// Sitzungen und benutzte Wiederherstellungscodes (docs/contracts/services.md, Aufbewahrung).
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Store löscht Verlauf in Batches; die einzige Ausnahme von „ein Fachpaket pro Tabelle“, weil
// Aufräumen eine eigene Operation über mehrere Verlaufstabellen ist.
type Store interface {
	PurgeFindings(ctx context.Context, endedBefore time.Time, limit int) (int, error)
	// PurgeRuns löscht abgeschlossene Lauf-Bäume über ihre Wurzel und zählt die Wurzeln.
	PurgeRuns(ctx context.Context, finishedBefore time.Time, limit int) (int, error)
	PurgeAudit(ctx context.Context, before time.Time, limit int) (int, error)
	// PurgeSessions löscht abgelaufene, widerrufene und lange unbenutzte Sitzungen sowie benutzte
	// Wiederherstellungscodes.
	PurgeSessions(ctx context.Context, before time.Time) (int, error)
}

const (
	batchSize   = 1000
	batchPause  = 100 * time.Millisecond
	firstRun    = time.Minute
	sessionsAge = 30 * 24 * time.Hour
	nightHour   = 3
	nightMinute = 30
)

// Config ist die Aufbewahrung aus MONIKA_RETENTION_*; 0 heißt „nie löschen“.
type Config struct {
	Store    Store
	Findings time.Duration
	Runs     time.Duration
	Audit    time.Duration
	Log      *slog.Logger
}

// Cleaner löscht 1 min nach dem Start und dann täglich um 03:30 Uhr Ortszeit.
type Cleaner struct {
	cfg   Config
	now   func() time.Time
	pause time.Duration
}

func New(cfg Config) *Cleaner { return &Cleaner{cfg: cfg, now: time.Now, pause: batchPause} }

// Run läuft, bis ctx endet; ein laufender Batch wird dann abgebrochen.
func (c *Cleaner) Run(ctx context.Context) error {
	timer := time.NewTimer(firstRun)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			c.purge(ctx)
			timer.Reset(time.Until(nextRun(c.now())))
		}
	}
}

// nextRun ist der nächste Zeitpunkt 03:30 Uhr Ortszeit nach now.
func nextRun(now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), nightHour, nightMinute, 0, 0, now.Location())
	if !t.After(now) {
		t = time.Date(now.Year(), now.Month(), now.Day()+1, nightHour, nightMinute, 0, 0, now.Location())
	}
	return t
}

// purge löscht alles, was älter ist als eingestellt, in Batches, und loggt die Anzahl.
func (c *Cleaner) purge(ctx context.Context) {
	now := c.now()
	kinds := []struct {
		name  string
		age   time.Duration
		purge func(context.Context, time.Time, int) (int, error)
	}{
		{"findings", c.cfg.Findings, c.cfg.Store.PurgeFindings},
		{"runs", c.cfg.Runs, c.cfg.Store.PurgeRuns},
		{"audit", c.cfg.Audit, c.cfg.Store.PurgeAudit},
	}
	for _, k := range kinds {
		if k.age <= 0 {
			continue
		}
		n, err := c.batches(ctx, now.Add(-k.age), k.purge)
		c.report(k.name, n, err)
	}
	n, err := c.cfg.Store.PurgeSessions(ctx, now.Add(-sessionsAge))
	c.report("sessions", n, err)
}

func (c *Cleaner) batches(ctx context.Context, before time.Time, purge func(context.Context, time.Time, int) (int, error)) (int, error) {
	total := 0
	for {
		n, err := purge(ctx, before, batchSize)
		total += n
		if err != nil {
			return total, err
		}
		if n < batchSize {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(c.pause):
		}
	}
}

func (c *Cleaner) report(what string, n int, err error) {
	switch {
	case err != nil:
		c.cfg.Log.Warn("retention failed", "what", what, "deleted", n, "err", fmt.Errorf("purge %s: %w", what, err))
	case n > 0:
		c.cfg.Log.Info("retention", "what", what, "deleted", n)
	}
}
