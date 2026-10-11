package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/sdk"
)

// ErrSourceNotRunning bedeutet, dass die Quelle gerade nicht läuft; der Lauf endet als failed.
var ErrSourceNotRunning = errors.New("source not running")

var errRunReturned = errors.New("Run returned without being stopped")

// CoreLink ist, was der Supervisor vom Core braucht.
type CoreLink interface {
	Reporter(sourceID uuid.UUID) sdk.Reporter
	Post(m core.Message)
}

// SecretValues liefert den Klartext eines Secrets (secret.Service).
type SecretValues interface {
	Value(ctx context.Context, id uuid.UUID) (json.RawMessage, error)
}

// Config enthält, was der Supervisor braucht; verdrahtet wird in cmd/monika. Den Core bekommt
// er erst in Run, denn der Core braucht seinerseits den Supervisor schon beim Erzeugen.
type Config struct {
	Secrets   SecretValues
	Scheduler *Scheduler
	Drivers   func(name string) (sdk.Driver, bool) // meist sdk.Lookup
	Log       *slog.Logger
}

// Zeiten aus docs/contracts/services.md und sdk.md.
const (
	stopTimeout = 10 * time.Second // danach gilt eine Quelle als Stuck
	minBackoff  = time.Second
	maxBackoff  = time.Minute
	stableAfter = time.Minute // so lange ohne Fehler, dann beginnt die Wartezeit wieder bei minBackoff
)

// Supervisor startet und stoppt die Quellen im Auftrag der Schleife: eine Goroutine pro Quelle
// verarbeitet deren Aufträge in Reihenfolge, nie laufen zwei Instanzen derselben Quelle.
type Supervisor struct {
	cfg Config

	stopTimeout, minBackoff, maxBackoff, stableAfter time.Duration

	mu      sync.Mutex
	ctx     context.Context // gesetzt von Run
	link    CoreLink        // gesetzt von Run, vor der ersten Goroutine eines Workers
	workers map[uuid.UUID]*worker
	running map[uuid.UUID]*instance // laufende, nicht gestoppte Instanzen für Execute
	wg      sync.WaitGroup
}

var _ core.Supervisor = (*Supervisor)(nil)

// New liefert einen Supervisor; Aufträge vor Run warten, bis Run läuft.
func New(cfg Config) *Supervisor {
	return &Supervisor{
		cfg:         cfg,
		stopTimeout: stopTimeout, minBackoff: minBackoff, maxBackoff: maxBackoff, stableAfter: stableAfter,
		workers: map[uuid.UUID]*worker{},
		running: map[uuid.UUID]*instance{},
	}
}

// Run arbeitet die Aufträge ab und meldet an core, bis ctx endet. Dann stoppt es alle Quellen
// parallel, wartet höchstens 10 s und meldet die übrigen als Stuck (Phase 3 des Herunterfahrens).
func (s *Supervisor) Run(ctx context.Context, core CoreLink) error {
	s.mu.Lock()
	s.ctx, s.link = ctx, core
	for _, w := range s.workers {
		s.spawn(w)
	}
	s.mu.Unlock()
	<-ctx.Done()
	s.wg.Wait()
	return nil
}

// Start beauftragt den Start einer Quelle; auf eine laufende ist es ein Neustart. Es kehrt
// sofort zurück.
func (s *Supervisor) Start(spec core.SourceSpec) { s.order(spec.SourceID, command{start: &spec}) }

// Stop beauftragt den Stopp einer Quelle; es kehrt sofort zurück.
func (s *Supervisor) Stop(sourceID uuid.UUID) { s.order(sourceID, command{}) }

// Execute führt einen Befehl an der laufenden Instanz der Quelle aus, hinter einer
// recover-Grenze. Nach einer Panic liefert es einen Fehler und startet die Quelle neu.
func (s *Supervisor) Execute(ctx context.Context, sourceID uuid.UUID, c sdk.Command) (res sdk.Result, err error) {
	s.mu.Lock()
	inst := s.running[sourceID]
	s.mu.Unlock()
	if inst == nil {
		return sdk.Result{}, ErrSourceNotRunning
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic in Execute: %v", p)
			res = sdk.Result{}
			inst.log.Error("source panicked in Execute", "panic", p)
			s.order(sourceID, command{fail: err, inst: inst})
		}
	}()
	return inst.src.Execute(ctx, c)
}

// order stellt einen Auftrag in die Liste der Quelle; ihre Goroutine entsteht bei Bedarf.
func (s *Supervisor) order(id uuid.UUID, c command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workers[id]
	if w == nil {
		w = &worker{s: s, id: id, signal: make(chan struct{}, 1)}
		s.workers[id] = w
		if s.ctx != nil && s.ctx.Err() == nil {
			s.spawn(w)
		}
	}
	w.mu.Lock()
	w.cmds = append(w.cmds, c)
	w.mu.Unlock()
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

// spawn startet die Goroutine eines Workers; s.mu ist gesperrt.
func (s *Supervisor) spawn(w *worker) {
	ctx := s.ctx
	s.wg.Go(func() { w.run(ctx) })
}

// command ist ein Auftrag: start (Spezifikation), Stopp (alles leer) oder fail (Panic in
// Execute bzw. einer Aufgabe des Schedulers bei Instanz inst).
type command struct {
	start *core.SourceSpec
	fail  error
	inst  *instance
}

type instance struct {
	src      sdk.Source
	log      *slog.Logger
	cancel   context.CancelFunc
	done     chan error // Ergebnis von Run
	started  time.Time
	stopping bool
	stuck    bool
	fail     error // Panic außerhalb von Run: beim Zurückkehren als SourceFailed melden
}

type worker struct {
	s      *Supervisor
	id     uuid.UUID
	mu     sync.Mutex
	cmds   []command
	signal chan struct{}

	// Ab hier gehört alles der Goroutine des Workers.
	desired *core.SourceSpec
	inst    *instance
	backoff time.Duration
	retry   *time.Timer
	stuck   *time.Timer
}

func (w *worker) run(ctx context.Context) {
	defer w.stopTimers()
	for {
		var done <-chan error
		if w.inst != nil {
			done = w.inst.done
		}
		select {
		case <-ctx.Done():
			w.shutdown()
			return
		case <-w.signal:
			for _, c := range w.take() {
				w.apply(ctx, c)
			}
		case err := <-done:
			w.returned(ctx, err)
		case <-timerC(w.retry):
			w.retry = nil
			if w.desired != nil && w.inst == nil {
				w.launch(ctx)
			}
		case <-timerC(w.stuck):
			w.stuck = nil
			if w.inst != nil && w.inst.stopping {
				w.inst.stuck = true
				w.inst.log.Error("source does not stop")
				w.s.link.Post(core.SourceStuck{SourceID: w.id})
			}
		}
		if w.idle() {
			return
		}
	}
}

func (w *worker) take() []command {
	w.mu.Lock()
	defer w.mu.Unlock()
	cmds := w.cmds
	w.cmds = nil
	return cmds
}

// idle beendet den Worker einer gestoppten Quelle ohne offene Aufträge.
func (w *worker) idle() bool {
	if w.desired != nil || w.inst != nil {
		return false
	}
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.cmds) > 0 {
		return false
	}
	delete(w.s.workers, w.id)
	return true
}

func (w *worker) apply(ctx context.Context, c command) {
	switch {
	case c.fail != nil:
		if w.inst == c.inst && w.inst != nil && !w.inst.stopping {
			w.inst.fail = c.fail
			w.stopInstance()
		}
	case c.start != nil:
		w.desired = c.start
		w.cancelRetry()
		switch {
		case w.inst == nil:
			w.backoff = 0
			w.launch(ctx)
		case !w.inst.stopping:
			w.stopInstance() // Neustart, sobald Run zurückkehrt
		}
	default:
		w.desired = nil
		w.cancelRetry()
		if w.inst != nil && !w.inst.stopping {
			w.stopInstance()
		}
	}
}

// launch startet eine Instanz: Secret holen, New, Heartbeat, Reporter, SourceStarted, Run
// (docs/contracts/services.md, Reihenfolge pro Start).
func (w *worker) launch(ctx context.Context) {
	spec := *w.desired
	s := w.s
	log := s.cfg.Log.With("driver", spec.Driver, "source_id", spec.SourceID, "device_id", spec.DeviceID)
	drv, ok := s.cfg.Drivers(spec.Driver)
	if !ok {
		w.failed(log, fmt.Errorf("driver %q is not registered", spec.Driver), false)
		return
	}
	var secret json.RawMessage
	if spec.SecretID != uuid.Nil {
		v, err := s.cfg.Secrets.Value(ctx, spec.SecretID)
		if err != nil {
			w.failed(log, fmt.Errorf("secret: %w", err), false)
			return
		}
		secret = v
	}
	inst := &instance{log: log, done: make(chan error, 1), started: time.Now()}
	cfg := sdk.SourceConfig{
		Address: spec.Address, Config: spec.Config, Secret: secret, Logger: log,
		Scheduler: s.cfg.Scheduler.For(spec.SourceID, log, func(err error) {
			s.order(w.id, command{fail: err, inst: inst})
		}),
	}
	src, heartbeat, err := newSource(drv, cfg)
	if err != nil {
		w.failed(log, err, errors.Is(err, errPanic))
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	inst.src, inst.cancel = src, cancel
	w.inst = inst
	s.mu.Lock()
	s.running[w.id] = inst // vor SourceStarted: wer davon hört, kann Execute rufen
	s.mu.Unlock()
	rep := s.link.Reporter(spec.SourceID)
	s.link.Post(core.SourceStarted{SourceID: spec.SourceID, Heartbeat: heartbeat})
	go func() { inst.done <- runSource(runCtx, src, rep) }()
}

var errPanic = errors.New("panic")

// newSource ruft New und Heartbeat hinter einer recover-Grenze.
func newSource(drv sdk.Driver, cfg sdk.SourceConfig) (src sdk.Source, heartbeat time.Duration, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%w in New: %v", errPanic, p)
		}
	}()
	if src, err = drv.New(cfg); err != nil {
		return nil, 0, fmt.Errorf("new: %w", err)
	}
	return src, src.Heartbeat(), nil
}

// runSource ruft Run hinter einer recover-Grenze.
func runSource(ctx context.Context, src sdk.Source, rep sdk.Reporter) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%w in Run: %v", errPanic, p)
		}
	}()
	return src.Run(ctx, rep)
}

// returned verarbeitet das Ende von Run.
func (w *worker) returned(ctx context.Context, err error) {
	inst := w.inst
	w.inst = nil
	w.stopStuck()
	w.s.mu.Lock()
	if w.s.running[w.id] == inst {
		delete(w.s.running, w.id)
	}
	w.s.mu.Unlock()
	inst.cancel()
	switch {
	case inst.fail != nil:
		w.failedRun(inst, inst.fail, true)
	case inst.stopping:
		if inst.stuck {
			inst.log.Warn("stuck source returned")
		}
		if err != nil {
			inst.log.Warn("source ended with an error while stopping", "err", err)
		}
		w.s.link.Post(core.SourceStopped{SourceID: w.id})
		if w.desired != nil {
			w.backoff = 0
			w.launch(ctx)
		}
	case err != nil:
		w.failedRun(inst, err, errors.Is(err, errPanic))
	default:
		w.failedRun(inst, errRunReturned, false)
	}
}

// failedRun meldet das Scheitern einer Instanz; lief sie lange genug, beginnt die Wartezeit neu.
func (w *worker) failedRun(inst *instance, err error, panicked bool) {
	if time.Since(inst.started) >= w.s.stableAfter {
		w.backoff = 0
	}
	w.failed(inst.log, err, panicked)
}

// failed meldet SourceFailed und plant den Neustart nach der Wartezeit.
func (w *worker) failed(log *slog.Logger, err error, panicked bool) {
	if panicked {
		log.Error("source panicked", "err", err)
	} else {
		log.Warn("source failed", "err", err)
	}
	w.s.link.Post(core.SourceFailed{SourceID: w.id, Err: err, Panic: panicked})
	if w.desired == nil {
		return
	}
	w.backoff = min(max(2*w.backoff, w.s.minBackoff), w.s.maxBackoff)
	w.retry = time.NewTimer(w.backoff)
}

func (w *worker) stopInstance() {
	w.inst.stopping = true
	w.inst.cancel()
	w.s.mu.Lock()
	if w.s.running[w.id] == w.inst {
		delete(w.s.running, w.id)
	}
	w.s.mu.Unlock()
	w.stuck = time.NewTimer(w.s.stopTimeout)
}

// shutdown stoppt beim Ende von Run die laufende Instanz und wartet höchstens stopTimeout.
func (w *worker) shutdown() {
	if w.inst == nil {
		return
	}
	if !w.inst.stopping {
		w.stopInstance()
	}
	if w.inst.stuck {
		return // schon als Stuck gemeldet
	}
	if w.stuck == nil {
		w.stuck = time.NewTimer(w.s.stopTimeout)
	}
	select {
	case <-w.inst.done:
		w.s.link.Post(core.SourceStopped{SourceID: w.id})
	case <-timerC(w.stuck):
		w.inst.log.Error("source does not stop")
		w.s.link.Post(core.SourceStuck{SourceID: w.id})
	}
}

func (w *worker) cancelRetry() {
	if w.retry != nil {
		w.retry.Stop()
		w.retry = nil
	}
}

func (w *worker) stopStuck() {
	if w.stuck != nil {
		w.stuck.Stop()
		w.stuck = nil
	}
}

func (w *worker) stopTimers() {
	w.cancelRetry()
	w.stopStuck()
}

// timerC liefert den Kanal eines Timers oder nil (blockiert im select für immer).
func timerC(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}
