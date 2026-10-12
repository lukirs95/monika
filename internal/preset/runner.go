package preset

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Fehler des Runners; die API macht daraus 4xx-Antworten.
var (
	ErrStopped        = errors.New("preset runner stopped")
	ErrForbidden      = errors.New("missing permission for a preset target")
	ErrDriverRequired = errors.New("device level needs a driver")
	ErrInvalidStep    = errors.New("invalid preset step")
)

// Missing ist ein fehlendes Recht an einem Ziel eines Schritts.
type Missing struct {
	StepID     uuid.UUID
	ElementID  uuid.UUID
	Permission auth.Permission
}

// MissingRightsError nennt alle fehlenden Rechte eines Presets (API: 403 missing_rights);
// errors.Is(err, ErrForbidden) gilt.
type MissingRightsError struct{ Missing []Missing }

func (e *MissingRightsError) Error() string {
	return fmt.Sprintf("%v: %d missing", ErrForbidden, len(e.Missing))
}
func (e *MissingRightsError) Is(target error) bool { return target == ErrForbidden }

const reasonStopped = "monika stopped"

// Executor ist, was der Runner vom Executor braucht.
type Executor interface {
	Targets(v *core.View, r executor.Request) ([]executor.Target, error)
	Submit(ctx context.Context, r executor.Request) (uuid.UUID, error)
	Cancel(ctx context.Context, ids []uuid.UUID) error
	Finish(ctx context.Context, id uuid.UUID, status action.Status, resp json.RawMessage) error
	Result(id uuid.UUID) (action.Run, bool)
}

// Core ist, was der Runner vom Core braucht.
type Core interface {
	View() *core.View
	Subscribe(buffer int) (<-chan core.Event, func())
}

// Permissions prüft Rechte (auth.Service).
type Permissions interface {
	Allowed(v *core.View, p auth.Principal, perm auth.Permission, element uuid.UUID) bool
}

// Config enthält, was der Runner braucht; verdrahtet wird in cmd/monika.
type Config struct {
	Store       Store
	Executor    Executor
	Core        Core
	Permissions Permissions
	Log         *slog.Logger
}

// Runner verwaltet Presets und führt sie aus: Ziele beim Start einfrieren, Rechte vorab prüfen,
// Stufen nacheinander, Schritte einer Stufe parallel (docs/contracts/flows.md, Preset-Lauf).
type Runner struct {
	cfg Config

	mu      sync.Mutex
	ctx     context.Context // von Run
	stopped bool
	active  map[uuid.UUID]*presetRun // Wurzel-Lauf → laufendes Preset
	wg      sync.WaitGroup
}

type presetRun struct {
	root   uuid.UUID
	events chan action.Run // RunChanged der Schritt-Läufe
	resync chan struct{}   // Ereignisse gingen verloren
	cancel context.CancelFunc
	user   bool // vom Benutzer abgebrochen
}

// NewRunner liefert einen Runner; Start gelingt erst, wenn Run läuft.
func NewRunner(cfg Config) *Runner {
	return &Runner{cfg: cfg, active: map[uuid.UUID]*presetRun{}}
}

// List, Get, CreatePreset, UpdatePreset und DeletePreset verwalten Presets; nur der Runner ruft
// den preset.Store.
func (r *Runner) List(ctx context.Context) ([]Preset, error) { return r.cfg.Store.ListPresets(ctx) }

func (r *Runner) Get(ctx context.Context, id uuid.UUID) (Preset, error) {
	return r.cfg.Store.GetPreset(ctx, id)
}

// CreatePreset legt ein Preset an; die ID vergibt die API, Schritte ohne ID bekommen eine.
func (r *Runner) CreatePreset(ctx context.Context, actor uuid.UUID, p Preset) error {
	if err := prepare(&p); err != nil {
		return err
	}
	return r.cfg.Store.CreatePreset(ctx, actor, p)
}

// UpdatePreset ersetzt Name, Beschreibung und Schritte; neue Schritte ohne ID bekommen eine.
func (r *Runner) UpdatePreset(ctx context.Context, actor uuid.UUID, p Preset) error {
	if err := prepare(&p); err != nil {
		return err
	}
	return r.cfg.Store.UpdatePreset(ctx, actor, p)
}

func (r *Runner) DeletePreset(ctx context.Context, actor, id uuid.UUID) error {
	return r.cfg.Store.DeletePreset(ctx, actor, id)
}

// prepare prüft ein Preset und vergibt fehlende IDs der Schritte.
func prepare(p *Preset) error {
	var errs []error
	if p.ID == uuid.Nil || p.Name == "" {
		errs = append(errs, fmt.Errorf("%w: preset needs an ID and a name", ErrInvalidStep))
	}
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.ID == uuid.Nil {
			s.ID = uuid.Must(uuid.NewV7())
		}
		switch {
		case s.Stage < 1 || s.Delay < 0 || s.Action == "" || s.ElementID == uuid.Nil:
			errs = append(errs, fmt.Errorf("%w: step %d needs a stage ≥ 1, a delay ≥ 0, an action and a target", ErrInvalidStep, i+1))
		case s.DeviceLevel && s.Driver == "":
			errs = append(errs, fmt.Errorf("step %d: %w", i+1, ErrDriverRequired))
		}
	}
	return errors.Join(errs...)
}

// Start friert die Ziele aller Schritte ein, prüft an ihnen die Rechte (operate, bei set
// configure, dazu critical) und startet das Preset; fehlt eines, startet nichts, und
// *MissingRightsError nennt alle fehlenden. Das Recht preset_run prüft die API.
func (r *Runner) Start(ctx context.Context, p auth.Principal, presetID uuid.UUID) (uuid.UUID, error) {
	r.mu.Lock()
	ready := r.ctx != nil && !r.stopped
	r.mu.Unlock()
	if !ready {
		return uuid.Nil, ErrStopped
	}
	preset, err := r.cfg.Store.GetPreset(ctx, presetID)
	if err != nil {
		return uuid.Nil, err
	}
	v := r.cfg.Core.View()
	targets := map[uuid.UUID][]uuid.UUID{}
	var missing []Missing
	for _, s := range preset.Steps {
		ts, err := r.cfg.Executor.Targets(v, request(p.UserID, preset.ID, s))
		if err != nil {
			return uuid.Nil, fmt.Errorf("stage %d, step %q on %s: %w", s.Stage, s.Action, v.Graph.Path(s.ElementID), err)
		}
		perm := auth.PermOperate
		if s.Action == vocab.ActionSet {
			perm = auth.PermConfigure
		}
		for _, t := range ts {
			if !r.cfg.Permissions.Allowed(v, p, perm, t.ElementID) {
				missing = append(missing, Missing{StepID: s.ID, ElementID: t.ElementID, Permission: perm})
			}
			if t.Critical && !r.cfg.Permissions.Allowed(v, p, auth.PermCritical, t.ElementID) {
				missing = append(missing, Missing{StepID: s.ID, ElementID: t.ElementID, Permission: auth.PermCritical})
			}
			targets[s.ID] = append(targets[s.ID], t.ElementID)
		}
	}
	if len(missing) > 0 {
		return uuid.Nil, &MissingRightsError{Missing: missing}
	}
	root, err := r.cfg.Executor.Submit(ctx, executor.Request{Actor: p.UserID, Action: executor.ActionPreset, PresetID: preset.ID})
	if err != nil {
		return uuid.Nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pctx, cancel := context.WithCancel(r.ctx)
	pr := &presetRun{root: root, events: make(chan action.Run, 1024), resync: make(chan struct{}, 1), cancel: cancel}
	r.active[root] = pr
	r.wg.Go(func() { r.execute(pctx, pr, p.UserID, preset, targets) })
	return root, nil
}

// Cancel bricht ein laufendes Preset ab: keine weiteren Stufen, Wartezeiten enden, offene Läufe
// werden abgebrochen; der Wurzel-Lauf endet als cancelled.
func (r *Runner) Cancel(_ context.Context, root uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	pr, ok := r.active[root]
	if !ok {
		return fmt.Errorf("cancel preset run %s: %w", root, executor.ErrFinished)
	}
	pr.user = true
	pr.cancel()
	return nil
}

// Run verteilt RunChanged an die laufenden Presets, bis ctx endet. Dann startet nichts Neues
// mehr, die laufenden Presets brechen ab, und Run verteilt weiter, bis sie beendet sind (sie
// warten auf das Ende ihrer abgebrochenen Läufe).
func (r *Runner) Run(ctx context.Context) error {
	events, unsubscribe := r.cfg.Core.Subscribe(1 << 14)
	defer func() { unsubscribe() }()
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
	var done chan struct{}
	stopping := ctx.Done()
	for {
		select {
		case <-stopping:
			stopping = nil
			r.mu.Lock()
			r.stopped = true
			r.mu.Unlock()
			done = make(chan struct{})
			go func() {
				r.wg.Wait()
				close(done)
			}()
		case <-done:
			return nil
		case ev, ok := <-events:
			if !ok { // abgehängt: neu anmelden, die Presets gleichen mit View und Executor ab
				events, unsubscribe = r.cfg.Core.Subscribe(1 << 14)
				r.resyncAll()
				continue
			}
			if rc, ok := ev.(core.RunChanged); ok && rc.Run.Finished() {
				r.deliver(rc.Run)
			}
		}
	}
}

func (r *Runner) deliver(run action.Run) {
	r.mu.Lock()
	pr := r.active[run.ParentID]
	r.mu.Unlock()
	if pr == nil {
		return
	}
	select {
	case pr.events <- run:
	default:
		r.signal(pr)
	}
}

func (r *Runner) resyncAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, pr := range r.active {
		r.signal(pr)
	}
}

func (r *Runner) signal(pr *presetRun) {
	select {
	case pr.resync <- struct{}{}:
	default:
	}
}

// execute arbeitet die Stufen ab und beendet den Wurzel-Lauf.
func (r *Runner) execute(ctx context.Context, pr *presetRun, actor uuid.UUID, p Preset, targets map[uuid.UUID][]uuid.UUID) {
	defer func() {
		r.mu.Lock()
		delete(r.active, pr.root)
		r.mu.Unlock()
		pr.cancel()
	}()
	steps := slices.Clone(p.Steps)
	slices.SortFunc(steps, func(a, b Step) int { return cmp.Or(cmp.Compare(a.Stage, b.Stage), cmp.Compare(a.Position, b.Position)) })
	var stages [][]Step
	for _, s := range steps {
		if n := len(stages); n == 0 || stages[n-1][0].Stage != s.Stage {
			stages = append(stages, nil)
		}
		stages[len(stages)-1] = append(stages[len(stages)-1], s)
	}
	counts := map[action.Status]int{}
	completed := 0
	for _, stage := range stages {
		if ctx.Err() != nil {
			break
		}
		failed := r.stage(ctx, pr, actor, p.ID, stage, targets, counts)
		if ctx.Err() != nil {
			break
		}
		completed++
		if failed {
			break
		}
	}
	status := worst(counts)
	r.mu.Lock()
	user, stopped := pr.user, r.stopped
	r.mu.Unlock()
	resp := map[string]any{"stages": len(stages), "completed_stages": completed, "statuses": counts}
	switch {
	case user:
		status = action.StatusCancelled
	case stopped && completed < len(stages):
		status, resp["error"] = action.StatusFailed, reasonStopped
	}
	b, _ := json.Marshal(resp)
	if err := r.cfg.Executor.Finish(context.WithoutCancel(ctx), pr.root, status, b); err != nil {
		r.cfg.Log.Error("preset run not finished", "run", pr.root, "err", err)
	}
}

type submitted struct {
	step Step
	id   uuid.UUID
	err  error
}

// stage startet die Schritte einer Stufe nach ihrer Wartezeit und wartet auf das Ende aller ihrer
// Läufe. Ein Schritt, der mit failed oder rejected endet, bricht ohne continue_on_error die Stufe
// ab: wartende Schritte entfallen, offene Läufe werden abgebrochen.
func (r *Runner) stage(ctx context.Context, pr *presetRun, actor, presetID uuid.UUID, stage []Step, targets map[uuid.UUID][]uuid.UUID, counts map[action.Status]int) (failed bool) {
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	results := make(chan submitted, len(stage))
	for _, s := range stage {
		go func() {
			timer := time.NewTimer(s.Delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-sctx.Done():
				results <- submitted{step: s, err: sctx.Err()}
				return
			}
			req := request(actor, presetID, s)
			req.Targets, req.ParentID, req.PresetStepID = targets[s.ID], pr.root, s.ID
			id, err := r.cfg.Executor.Submit(context.WithoutCancel(sctx), req)
			results <- submitted{step: s, id: id, err: err}
		}()
	}
	awaiting := map[uuid.UUID]Step{}
	early := map[uuid.UUID]action.Run{} // Ende kam vor der ID des Laufs
	open := len(stage)
	record := func(run action.Run, s Step) {
		delete(awaiting, run.ID)
		counts[run.Status]++
		if (run.Status == action.StatusFailed || run.Status == action.StatusRejected) && !s.ContinueOnError && !failed {
			failed = true
			stop()
			r.cancelRuns(awaiting)
		}
	}
	cancelled := false
	for open > 0 || len(awaiting) > 0 {
		select {
		case res := <-results:
			open--
			switch {
			case res.err != nil && sctx.Err() != nil: // entfallen
			case res.err != nil:
				r.cfg.Log.Warn("preset step not submitted", "run", pr.root, "step", res.step.ID, "err", res.err)
				record(action.Run{Status: action.StatusFailed}, res.step)
			default:
				awaiting[res.id] = res.step
				if run, ok := early[res.id]; ok {
					record(run, res.step)
				} else if failed || cancelled {
					r.cancelRuns(map[uuid.UUID]Step{res.id: res.step})
				}
			}
		case run := <-pr.events:
			if s, ok := awaiting[run.ID]; ok {
				record(run, s)
			} else {
				early[run.ID] = run
			}
		case <-pr.resync:
			views := r.cfg.Core.View().Runs
			for id, s := range awaiting {
				if _, running := views[id]; running {
					continue
				}
				if run, ok := r.cfg.Executor.Result(id); ok {
					record(run, s)
				}
			}
		case <-ctx.Done():
			if !cancelled {
				cancelled = true
				stop()
				r.cancelRuns(awaiting)
			}
			ctx = context.Background() // nur einmal abbrechen, dann auf das Ende der Läufe warten
		}
	}
	return failed
}

func (r *Runner) cancelRuns(runs map[uuid.UUID]Step) {
	ids := make([]uuid.UUID, 0, len(runs))
	for id := range runs {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return
	}
	if err := r.cfg.Executor.Cancel(context.Background(), ids); err != nil && !errors.Is(err, executor.ErrFinished) {
		r.cfg.Log.Warn("preset runs not cancelled", "err", err)
	}
}

func request(actor, presetID uuid.UUID, s Step) executor.Request {
	return executor.Request{
		Actor: actor, ElementID: s.ElementID, Action: s.Action, Params: s.Params,
		DeviceLevel: s.DeviceLevel, Driver: s.Driver, PresetID: presetID,
	}
}

// worst ist der schlechteste Status der Schritte: failed > rejected > unconfirmed > accepted >
// confirmed; ohne Schritt-Läufe cancelled.
func worst(counts map[action.Status]int) action.Status {
	for _, s := range []action.Status{action.StatusFailed, action.StatusRejected, action.StatusUnconfirmed, action.StatusAccepted, action.StatusConfirmed} {
		if counts[s] > 0 {
			return s
		}
	}
	return action.StatusCancelled
}
