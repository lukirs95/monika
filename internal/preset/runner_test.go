package preset

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk/vocab"
)

// fakeExecutor: Targets liefert pro Ziel-Element ein Element (kritisch bei reboot); Submit meldet
// den Schritt auf submitted; Cancel beendet die Läufe als cancelled.
type fakeExecutor struct {
	mu        sync.Mutex
	submitted chan executor.Request
	ids       map[uuid.UUID]executor.Request
	root      uuid.UUID
	finished  chan action.Run
	cancelled []uuid.UUID
	results   map[uuid.UUID]action.Run
	core      *fakeCore
	targetErr error
}

func (f *fakeExecutor) Targets(_ *core.View, r executor.Request) ([]executor.Target, error) {
	if f.targetErr != nil {
		return nil, f.targetErr
	}
	return []executor.Target{{ElementID: r.ElementID, Critical: r.Action == vocab.ActionReboot}}, nil
}

func (f *fakeExecutor) Submit(_ context.Context, r executor.Request) (uuid.UUID, error) {
	id := uuid.New()
	f.mu.Lock()
	f.ids[id] = r
	if r.Action == executor.ActionPreset {
		f.root = id
	}
	f.mu.Unlock()
	if r.Action != executor.ActionPreset {
		f.submitted <- r
	}
	return id, nil
}

func (f *fakeExecutor) Cancel(_ context.Context, ids []uuid.UUID) error {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, ids...)
	f.mu.Unlock()
	for _, id := range ids {
		f.end(id, action.StatusCancelled)
	}
	return nil
}

func (f *fakeExecutor) Finish(_ context.Context, id uuid.UUID, status action.Status, resp json.RawMessage) error {
	f.finished <- action.Run{ID: id, Status: status, Response: resp}
	return nil
}

func (f *fakeExecutor) Result(id uuid.UUID) (action.Run, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.results[id]
	return r, ok
}

// end beendet den Schritt-Lauf mit der ID und meldet RunChanged.
func (f *fakeExecutor) end(id uuid.UUID, status action.Status) {
	f.mu.Lock()
	run := action.Run{ID: id, ParentID: f.root, Status: status, FinishedAt: time.Now()}
	f.results[id] = run
	f.mu.Unlock()
	f.core.events <- core.RunChanged{Run: run}
}

// idOf liefert die ID des Laufs zu einem eingereichten Schritt.
func (f *fakeExecutor) idOf(step uuid.UUID) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, r := range f.ids {
		if r.PresetStepID == step {
			return id
		}
	}
	return uuid.Nil
}

type fakeCore struct {
	view   *core.View
	mu     sync.Mutex
	events chan core.Event
}

func (f *fakeCore) View() *core.View { return f.view }
func (f *fakeCore) Subscribe(int) (<-chan core.Event, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events, func() {}
}

// drop simuliert das Abhängen: Der Kanal schließt, die nächste Anmeldung bekommt einen neuen.
func (f *fakeCore) drop() {
	f.mu.Lock()
	old := f.events
	f.events = make(chan core.Event, 64)
	f.mu.Unlock()
	close(old)
}

type fakePerms struct{ deny auth.Permission }

func (f fakePerms) Allowed(_ *core.View, _ auth.Principal, perm auth.Permission, _ uuid.UUID) bool {
	return perm != f.deny
}

type fakeStore struct {
	Store
	presets map[uuid.UUID]Preset
	saved   []Preset
}

func (f *fakeStore) GetPreset(_ context.Context, id uuid.UUID) (Preset, error) {
	p, ok := f.presets[id]
	if !ok {
		return Preset{}, ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) CreatePreset(_ context.Context, _ uuid.UUID, p Preset) error {
	f.saved = append(f.saved, p)
	return nil
}

type rig struct {
	t      *testing.T
	runner *Runner
	ex     *fakeExecutor
	core   *fakeCore
	store  *fakeStore
	stop   func()
}

func newRig(t *testing.T, perms fakePerms) *rig {
	t.Helper()
	fc := &fakeCore{view: &core.View{Graph: inventory.New(nil, nil, slog.New(slog.DiscardHandler)).Graph()}, events: make(chan core.Event, 64)}
	ex := &fakeExecutor{submitted: make(chan executor.Request, 16), ids: map[uuid.UUID]executor.Request{}, finished: make(chan action.Run, 1),
		results: map[uuid.UUID]action.Run{}, core: fc}
	r := &rig{t: t, ex: ex, core: fc, store: &fakeStore{presets: map[uuid.UUID]Preset{}}}
	r.runner = NewRunner(Config{Store: r.store, Executor: ex, Core: fc, Permissions: perms, Log: slog.New(slog.DiscardHandler)})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = r.runner.Run(ctx)
		close(done)
	}()
	r.stop = sync.OnceFunc(func() {
		cancel()
		<-done
	})
	t.Cleanup(r.stop)
	for {
		r.runner.mu.Lock()
		ready := r.runner.ctx != nil
		r.runner.mu.Unlock()
		if ready {
			return r
		}
		time.Sleep(time.Millisecond)
	}
}

// preset legt ein Preset mit den Schritten an (Stufe, Wartezeit, continue_on_error).
func (r *rig) preset(steps ...Step) Preset {
	p := Preset{ID: uuid.New(), Name: "Show"}
	for i, s := range steps {
		s.ID, s.ElementID, s.Position = uuid.New(), uuid.New(), i
		if s.Action == "" {
			s.Action = vocab.ActionStart
		}
		p.Steps = append(p.Steps, s)
	}
	r.store.presets[p.ID] = p
	return p
}

func (r *rig) start(p Preset) uuid.UUID {
	r.t.Helper()
	root, err := r.runner.Start(r.t.Context(), auth.Principal{UserID: uuid.New()}, p.ID)
	if err != nil {
		r.t.Fatalf("Start: %v", err)
	}
	return root
}

// next wartet auf den nächsten eingereichten Schritt.
func (r *rig) next() executor.Request {
	r.t.Helper()
	select {
	case req := <-r.ex.submitted:
		return req
	case <-time.After(2 * time.Second):
		r.t.Fatal("no step submitted")
	}
	return executor.Request{}
}

func (r *rig) none(d time.Duration) {
	r.t.Helper()
	select {
	case req := <-r.ex.submitted:
		r.t.Fatalf("unexpected step %+v", req)
	case <-time.After(d):
	}
}

func (r *rig) finished() action.Run {
	r.t.Helper()
	select {
	case run := <-r.ex.finished:
		return run
	case <-time.After(2 * time.Second):
		r.t.Fatal("root run not finished")
	}
	return action.Run{}
}

func TestStagesInOrder(t *testing.T) {
	r := newRig(t, fakePerms{})
	p := r.preset(Step{Stage: 1}, Step{Stage: 1, Delay: 30 * time.Millisecond}, Step{Stage: 2, Action: vocab.ActionSet})
	root := r.start(p)
	first := r.next()
	if first.PresetStepID != p.Steps[0].ID || first.ParentID != root || len(first.Targets) != 1 || first.Targets[0] != p.Steps[0].ElementID {
		t.Errorf("first step = %+v", first)
	}
	second := r.next() // nach der Wartezeit, noch in Stufe 1
	r.ex.end(r.ex.idOf(first.PresetStepID), action.StatusConfirmed)
	r.none(30 * time.Millisecond) // Stufe 2 wartet auf den zweiten Schritt
	r.ex.end(r.ex.idOf(second.PresetStepID), action.StatusUnconfirmed)
	third := r.next()
	r.ex.end(r.ex.idOf(third.PresetStepID), action.StatusConfirmed)
	run := r.finished()
	var sum struct {
		Stages    int                   `json:"stages"`
		Completed int                   `json:"completed_stages"`
		Statuses  map[action.Status]int `json:"statuses"`
	}
	if err := json.Unmarshal(run.Response, &sum); err != nil || run.ID != root || run.Status != action.StatusUnconfirmed ||
		sum.Stages != 2 || sum.Completed != 2 || sum.Statuses[action.StatusConfirmed] != 2 {
		t.Errorf("root = %s %s, %v", run.Status, run.Response, err)
	}
}

func TestFailureAborts(t *testing.T) {
	r := newRig(t, fakePerms{})
	p := r.preset(Step{Stage: 1}, Step{Stage: 1, Delay: time.Second}, Step{Stage: 2})
	r.start(p)
	first := r.next()
	r.ex.end(r.ex.idOf(first.PresetStepID), action.StatusFailed)
	if run := r.finished(); run.Status != action.StatusFailed {
		t.Errorf("root = %s", run.Status)
	}
	r.none(20 * time.Millisecond) // weder der verzögerte Schritt noch Stufe 2
}

func TestContinueOnError(t *testing.T) {
	r := newRig(t, fakePerms{})
	p := r.preset(Step{Stage: 1, ContinueOnError: true}, Step{Stage: 2})
	r.start(p)
	first := r.next()
	r.ex.end(r.ex.idOf(first.PresetStepID), action.StatusRejected)
	second := r.next()
	r.ex.end(r.ex.idOf(second.PresetStepID), action.StatusConfirmed)
	if run := r.finished(); run.Status != action.StatusRejected {
		t.Errorf("root = %s", run.Status)
	}
}

func TestStartRefused(t *testing.T) {
	r := newRig(t, fakePerms{deny: auth.PermCritical})
	p := r.preset(Step{Stage: 1}, Step{Stage: 2, Action: vocab.ActionReboot})
	_, err := r.runner.Start(t.Context(), auth.Principal{}, p.ID)
	missing, ok := errors.AsType[*MissingRightsError](err)
	if !errors.Is(err, ErrForbidden) || !ok || len(missing.Missing) != 1 ||
		missing.Missing[0].StepID != p.Steps[1].ID || missing.Missing[0].Permission != auth.PermCritical {
		t.Errorf("Start without critical = %v", err)
	}
	// Alle fehlenden Rechte, nicht nur das erste.
	r.runner.cfg.Permissions = fakePerms{deny: auth.PermOperate}
	if _, err := r.runner.Start(t.Context(), auth.Principal{}, p.ID); !errors.As(err, &missing) || len(missing.Missing) != 2 {
		t.Errorf("Start without operate = %v", err)
	}
	r.runner.cfg.Permissions = fakePerms{}
	r.ex.targetErr = executor.ErrUnsupported
	if _, err := r.runner.Start(t.Context(), auth.Principal{}, p.ID); !errors.Is(err, executor.ErrUnsupported) {
		t.Errorf("Start with unresolvable step = %v", err)
	}
	if r.ex.root != uuid.Nil {
		t.Error("root run created")
	}
	r.none(10 * time.Millisecond)
}

func TestCancelPreset(t *testing.T) {
	r := newRig(t, fakePerms{})
	p := r.preset(Step{Stage: 1}, Step{Stage: 2})
	root := r.start(p)
	r.next()
	if err := r.runner.Cancel(t.Context(), root); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if run := r.finished(); run.Status != action.StatusCancelled {
		t.Errorf("root = %s", run.Status)
	}
	if len(r.ex.cancelled) != 1 {
		t.Errorf("cancelled step runs = %v", r.ex.cancelled)
	}
	r.none(20 * time.Millisecond)
}

func TestResyncAfterDrop(t *testing.T) {
	r := newRig(t, fakePerms{})
	p := r.preset(Step{Stage: 1})
	r.start(p)
	step := r.next()
	id := r.ex.idOf(step.PresetStepID)
	// Das Ende geht verloren; der Runner fragt nach dem Neuanmelden den Executor.
	r.ex.mu.Lock()
	r.ex.results[id] = action.Run{ID: id, Status: action.StatusConfirmed, FinishedAt: time.Now()}
	r.ex.mu.Unlock()
	r.core.drop()
	if run := r.finished(); run.Status != action.StatusConfirmed {
		t.Errorf("root = %s", run.Status)
	}
}

func TestShutdown(t *testing.T) {
	r := newRig(t, fakePerms{})
	p := r.preset(Step{Stage: 1}, Step{Stage: 2})
	r.start(p)
	r.next()
	go r.stop()
	if run := r.finished(); run.Status != action.StatusCancelled && run.Status != action.StatusFailed {
		t.Errorf("root = %s", run.Status)
	}
	r.stop()
	if _, err := r.runner.Start(t.Context(), auth.Principal{}, p.ID); !errors.Is(err, ErrStopped) {
		t.Errorf("Start after stop = %v", err)
	}
}

func TestPrepare(t *testing.T) {
	r := newRig(t, fakePerms{})
	p := Preset{ID: uuid.New(), Name: "Show", Steps: []Step{{Stage: 1, ElementID: uuid.New(), Action: vocab.ActionStart}}}
	if err := r.runner.CreatePreset(t.Context(), uuid.New(), p); err != nil || r.store.saved[0].Steps[0].ID == uuid.Nil {
		t.Errorf("CreatePreset = %v, step ID %v", err, r.store.saved)
	}
	tests := []struct {
		name string
		step Step
		want error
	}{
		{"device level without driver", Step{Stage: 1, ElementID: uuid.New(), Action: vocab.ActionReboot, DeviceLevel: true}, ErrDriverRequired},
		{"stage 0", Step{ElementID: uuid.New(), Action: vocab.ActionStart}, ErrInvalidStep},
		{"no target", Step{Stage: 1, Action: vocab.ActionStart}, ErrInvalidStep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Preset{ID: uuid.New(), Name: "Show", Steps: []Step{tt.step}}
			if err := r.runner.CreatePreset(t.Context(), uuid.New(), p); !errors.Is(err, tt.want) {
				t.Errorf("CreatePreset = %v, want %v", err, tt.want)
			}
		})
	}
}
