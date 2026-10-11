package executor

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// fakeCore liefert eine veränderbare View, einen Ereigniskanal und zeichnet RunChanged auf.
type fakeCore struct {
	view    atomic.Pointer[core.View]
	events  chan core.Event
	mu      sync.Mutex
	runs    map[uuid.UUID][]action.Status
	lastRun map[uuid.UUID]action.Run
}

func (f *fakeCore) View() *core.View { return f.view.Load() }
func (f *fakeCore) Subscribe(int) (<-chan core.Event, func()) {
	return f.events, func() {}
}
func (f *fakeCore) Post(m core.Message) {
	if rc, ok := m.(core.RunChanged); ok {
		f.mu.Lock()
		f.runs[rc.Run.ID] = append(f.runs[rc.Run.ID], rc.Run.Status)
		f.lastRun[rc.Run.ID] = rc.Run
		f.mu.Unlock()
	}
}

func (f *fakeCore) statuses(id uuid.UUID) []action.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]action.Status(nil), f.runs[id]...)
}

// setFacts setzt Fakten eines Elements und meldet FactsChanged.
func (f *fakeCore) setFacts(id uuid.UUID, facts sdk.Facts) {
	v := *f.view.Load()
	v.Facts = map[uuid.UUID]sdk.Facts{id: facts}
	f.view.Store(&v)
	f.events <- core.FactsChanged{Changed: map[uuid.UUID][]string{id: {"x"}}}
}

// fakeSources antwortet nach answer; block hält Execute an, bis es geschlossen wird.
type fakeSources struct {
	mu     sync.Mutex
	answer func(c sdk.Command) (sdk.Result, error)
	block  chan struct{}
	calls  []string
}

func (f *fakeSources) Execute(ctx context.Context, _ uuid.UUID, c sdk.Command) (sdk.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c.Action+" "+c.ExternalID)
	answer, block := f.answer, f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return sdk.Result{}, ctx.Err()
		}
	}
	if answer == nil {
		return sdk.Result{Status: sdk.Accepted}, nil
	}
	return answer(c)
}

type fakeStore struct {
	mu      sync.Mutex
	created []action.Run
	updates map[uuid.UUID][]action.Status
}

func (f *fakeStore) CreateRuns(_ context.Context, runs []action.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, runs...)
	return nil
}

func (f *fakeStore) UpdateRun(_ context.Context, r action.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates[r.ID] = append(f.updates[r.ID], r.Status)
	return nil
}

func (f *fakeStore) FinishInterruptedRuns(context.Context, string) (int, error) { return 0, nil }

type rig struct {
	t       *testing.T
	w       *world
	ex      *Executor
	core    *fakeCore
	sources *fakeSources
	store   *fakeStore
	stop    func()
}

func newRig(t *testing.T) *rig {
	t.Helper()
	w := newWorld(t)
	r := &rig{t: t, w: w, core: &fakeCore{events: make(chan core.Event, 16), runs: map[uuid.UUID][]action.Status{}, lastRun: map[uuid.UUID]action.Run{}},
		sources: &fakeSources{}, store: &fakeStore{updates: map[uuid.UUID][]action.Status{}}}
	r.core.view.Store(w.view)
	drivers := map[string]sdk.Driver{
		"fake": {Name: "fake", Actions: []sdk.ActionDef{
			{Name: vocab.ActionStart, Verify: []sdk.Condition{{Fact: vocab.StateRunning, Op: "eq", Value: "true"}}, Timeout: 100 * time.Millisecond},
			{Name: vocab.ActionStop},
		}},
		"other": {Name: "other", Actions: []sdk.ActionDef{{Name: vocab.ActionStart}}},
	}
	r.ex = New(Config{Core: r.core, Sources: r.sources, Store: r.store, Log: slog.New(slog.DiscardHandler),
		Drivers: func(name string) (sdk.Driver, bool) { d, ok := drivers[name]; return d, ok }})
	r.ex.executeTimeout, r.ex.stopWait = 100*time.Millisecond, 100*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = r.ex.Run(ctx)
		close(done)
	}()
	r.stop = sync.OnceFunc(func() {
		cancel()
		<-done
	})
	t.Cleanup(r.stop)
	for { // bis Run bereit ist
		r.ex.mu.Lock()
		ready := r.ex.ctx != nil
		r.ex.mu.Unlock()
		if ready {
			return r
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *rig) submit(req Request) uuid.UUID {
	r.t.Helper()
	id, err := r.ex.Submit(r.t.Context(), req)
	if err != nil {
		r.t.Fatalf("Submit: %v", err)
	}
	return id
}

// wait wartet, bis der Lauf den Status erreicht, und liefert seinen Verlauf.
func (r *rig) wait(id uuid.UUID, want action.Status) []action.Status {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := r.core.statuses(id)
		if len(got) > 0 && got[len(got)-1] == want {
			return got
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("run %s: statuses %v, want %s", id, got, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func statuses(s ...action.Status) []action.Status { return s }

func sameStatuses(a, b []action.Status) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestExecuteConfirmed(t *testing.T) {
	r := newRig(t)
	id := r.submit(Request{ElementID: r.w.m, Action: vocab.ActionStart})
	r.wait(id, action.StatusAccepted)
	r.core.setFacts(r.w.m, sdk.Facts{vocab.StateRunning: true})
	got := r.wait(id, action.StatusConfirmed)
	want := statuses(action.StatusPending, action.StatusSent, action.StatusAccepted, action.StatusConfirmed)
	if !sameStatuses(got, want) {
		t.Errorf("RunChanged = %v, want %v", got, want)
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if !sameStatuses(r.store.updates[id], want[1:]) {
		t.Errorf("UpdateRun = %v", r.store.updates[id])
	}
}

func TestExecuteOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		action string
		answer func(sdk.Command) (sdk.Result, error)
		block  bool
		want   action.Status
		text   string
	}{
		{"accepted without verify", vocab.ActionStop, nil, false, action.StatusAccepted, ""},
		{"unconfirmed", vocab.ActionStart, nil, false, action.StatusUnconfirmed, ""},
		{"rejected", vocab.ActionStop, func(sdk.Command) (sdk.Result, error) {
			return sdk.Result{Status: sdk.Rejected, RejectedFields: []string{"codec.format"}}, nil
		}, false, action.StatusRejected, "codec.format"},
		{"unreachable", vocab.ActionStop, func(sdk.Command) (sdk.Result, error) { return sdk.Result{}, errors.New("no route") }, false, action.StatusFailed, "no route"},
		{"timeout", vocab.ActionStop, nil, true, action.StatusFailed, "timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.sources.answer = tt.answer
			if tt.block {
				r.sources.block = make(chan struct{})
			}
			id := r.submit(Request{ElementID: r.w.m, Action: tt.action})
			r.wait(id, tt.want)
			if tt.text != "" {
				resp := lastResponse(t, r, id)
				if !strings.Contains(resp, tt.text) {
					t.Errorf("response = %s, want %q", resp, tt.text)
				}
			}
		})
	}
}

// lastResponse liefert die Antwort aus dem zuletzt gemeldeten Stand eines Laufs.
func lastResponse(t *testing.T, r *rig, id uuid.UUID) string {
	t.Helper()
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	return string(r.core.lastRun[id].Response)
}

func TestFIFOAndCancel(t *testing.T) {
	r := newRig(t)
	r.sources.block = make(chan struct{})
	first := r.submit(Request{ElementID: r.w.m, Action: vocab.ActionStop})
	r.wait(first, action.StatusSent)
	second := r.submit(Request{ElementID: r.w.m, Action: vocab.ActionStop})
	third := r.submit(Request{ElementID: r.w.m, Action: vocab.ActionStop})
	time.Sleep(20 * time.Millisecond)
	if got := r.core.statuses(second); !sameStatuses(got, statuses(action.StatusPending)) {
		t.Fatalf("second run = %v while the first is sent", got)
	}
	if err := r.ex.Cancel(t.Context(), []uuid.UUID{second, first}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	r.wait(second, action.StatusCancelled)
	close(r.sources.block)
	r.wait(first, action.StatusAccepted) // gesendet: läuft zu Ende
	r.wait(third, action.StatusAccepted)
	if err := r.ex.Cancel(t.Context(), []uuid.UUID{first}); !errors.Is(err, ErrFinished) {
		t.Errorf("Cancel of a finished run = %v", err)
	}
	r.sources.mu.Lock()
	defer r.sources.mu.Unlock()
	if len(r.sources.calls) != 2 {
		t.Errorf("Execute calls = %v, want first and third", r.sources.calls)
	}
}

func TestCollective(t *testing.T) {
	r := newRig(t)
	r.sources.answer = func(c sdk.Command) (sdk.Result, error) {
		return sdk.Result{Status: sdk.Accepted}, nil
	}
	// M bestätigt sofort, M2 (other) hat keine Prüfbedingung, P3 ist verschwunden.
	r.core.view.Store(&core.View{Graph: r.w.view.Graph, Facts: map[uuid.UUID]sdk.Facts{r.w.m: {vocab.StateRunning: true}}})
	id := r.submit(Request{ElementID: r.w.g, Action: vocab.ActionStart})
	r.wait(id, action.StatusFailed)
	var sum summary
	if err := json.Unmarshal([]byte(lastResponse(t, r, id)), &sum); err != nil || sum.Targets != 3 ||
		sum.Statuses[action.StatusConfirmed] != 1 || sum.Statuses[action.StatusAccepted] != 1 || sum.Statuses[action.StatusFailed] != 1 {
		t.Errorf("summary = %+v, %v", sum, err)
	}
}

func TestCancelCollective(t *testing.T) {
	r := newRig(t)
	r.sources.block = make(chan struct{})
	id := r.submit(Request{ElementID: r.w.g, Action: vocab.ActionStart, Driver: "other"})
	r.wait(id, action.StatusPending)
	time.Sleep(20 * time.Millisecond) // das Kind ist gesendet
	if err := r.ex.Cancel(t.Context(), []uuid.UUID{id}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(r.sources.block)
	r.wait(id, action.StatusCancelled)
}

func TestStop(t *testing.T) {
	r := newRig(t)
	r.sources.block = make(chan struct{})
	sent := r.submit(Request{ElementID: r.w.m, Action: vocab.ActionStop})
	r.wait(sent, action.StatusSent)
	pending := r.submit(Request{ElementID: r.w.m, Action: vocab.ActionStop})
	r.stop()
	r.wait(pending, action.StatusFailed)
	r.wait(sent, action.StatusFailed)
	if _, err := r.ex.Submit(t.Context(), Request{ElementID: r.w.m, Action: vocab.ActionStop}); !errors.Is(err, ErrStopped) {
		t.Errorf("Submit after stop = %v", err)
	}
	close(r.sources.block)
}

func TestStopUnconfirmsWaiting(t *testing.T) {
	r := newRig(t)
	id := r.submit(Request{ElementID: r.w.m, Action: vocab.ActionStart})
	r.wait(id, action.StatusAccepted)
	r.stop()
	r.wait(id, action.StatusUnconfirmed)
}

func TestSubmitBeforeRun(t *testing.T) {
	ex := New(Config{})
	if _, err := ex.Submit(t.Context(), Request{}); !errors.Is(err, ErrStopped) {
		t.Errorf("Submit before Run = %v", err)
	}
}

func TestPresetSupport(t *testing.T) {
	r := newRig(t)
	single, err := r.ex.Targets(r.w.view, Request{ElementID: r.w.m, Action: vocab.ActionStart})
	if err != nil || len(single) != 1 || single[0].ElementID != r.w.m {
		t.Fatalf("Targets(element) = %+v, %v", single, err)
	}
	group, err := r.ex.Targets(r.w.view, Request{ElementID: r.w.g, Action: vocab.ActionStart})
	if err != nil || len(group) != 3 {
		t.Fatalf("Targets(group) = %+v, %v", group, err)
	}

	root := r.submit(Request{Action: ActionPreset})
	step := r.submit(Request{ElementID: r.w.g, Action: vocab.ActionStop, Targets: []uuid.UUID{r.w.m}, ParentID: root})
	r.wait(step, action.StatusAccepted)
	if res, ok := r.ex.Result(step); !ok || res.Status != action.StatusAccepted {
		t.Errorf("Result(step) = %+v, %v", res, ok)
	}
	if err := r.ex.Finish(t.Context(), step, action.StatusConfirmed, nil); !errors.Is(err, ErrFinished) {
		t.Errorf("Finish on a step run = %v", err)
	}
	if err := r.ex.Finish(t.Context(), root, action.StatusAccepted, json.RawMessage(`{"stages":1}`)); err != nil {
		t.Fatalf("Finish(root) = %v", err)
	}
	r.wait(root, action.StatusAccepted)
	if _, ok := r.ex.Result(step); ok {
		t.Error("result kept after the preset finished")
	}
}
