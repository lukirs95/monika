package forward

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
)

type fakeStore struct {
	mu      sync.Mutex
	targets []Target
	results []Result
}

func (f *fakeStore) ListTargets(context.Context) ([]Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Target(nil), f.targets...), nil
}
func (f *fakeStore) CreateTarget(context.Context, uuid.UUID, Target) error    { return nil }
func (f *fakeStore) UpdateTarget(context.Context, uuid.UUID, Target) error    { return nil }
func (f *fakeStore) DeleteTarget(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeStore) SetTargetResults(_ context.Context, rs []Result) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, rs...)
	return nil
}

type fakeCore struct {
	view   atomic.Pointer[core.View]
	events chan core.Event
}

func (f *fakeCore) View() *core.View                          { return f.view.Load() }
func (f *fakeCore) Subscribe(int) (<-chan core.Event, func()) { return f.events, func() {} }

// hook sammelt die empfangenen Ereignisse; fail lässt Anfragen scheitern.
type hook struct {
	mu     sync.Mutex
	events []string
	fail   int // so viele Anfragen scheitern
}

func (h *hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fail > 0 {
		h.fail--
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	b, _ := io.ReadAll(r.Body)
	var m struct {
		Event   string `json:"event"`
		Finding *struct {
			ID uuid.UUID `json:"id"`
		} `json:"finding"`
	}
	_ = json.Unmarshal(b, &m)
	h.events = append(h.events, m.Event)
}

func (h *hook) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.events...)
}

type rig struct {
	t      *testing.T
	f      *Forwarder
	core   *fakeCore
	store  *fakeStore
	hook   *hook
	srv    *httptest.Server
	device uuid.UUID
	port   uuid.UUID
	other  uuid.UUID
	stop   func()
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, hook: &hook{}, store: &fakeStore{}, device: uuid.New(), port: uuid.New(), other: uuid.New()}
	r.srv = httptest.NewServer(r.hook)
	t.Cleanup(r.srv.Close)
	inv := inventory.New(nil, nil, slog.New(slog.DiscardHandler))
	inv.Load(inventory.Snapshot{Elements: []inventory.Element{
		{ID: r.device, Kind: inventory.KindDevice, DeviceID: r.device},
		{ID: r.port, Kind: "port", ParentID: r.device, DeviceID: r.device},
		{ID: r.other, Kind: inventory.KindDevice, DeviceID: r.other},
	}})
	r.core = &fakeCore{events: make(chan core.Event, 64)}
	r.core.view.Store(&core.View{Graph: inv.Graph()})
	r.f = New(Config{Store: r.store, Core: r.core, PublicURL: public, Log: slog.New(slog.DiscardHandler)})
	r.f.waits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	return r
}

// target liefert ein Ziel; stored legt es schon vor Run in den Store.
func (r *rig) target(stored bool, change ...func(*Target)) Target {
	t := Target{ID: uuid.New(), Name: "hook", Kind: KindWebhook, Enabled: true,
		Config: json.RawMessage(`{"url":"` + r.srv.URL + `"}`),
		Events: []string{EventFindingRaised, EventFindingCleared, EventFindingWithdrawn, EventActionFinished}}
	for _, c := range change {
		c(&t)
	}
	if stored {
		r.store.mu.Lock()
		r.store.targets = append(r.store.targets, t)
		r.store.mu.Unlock()
	}
	return t
}

func (r *rig) run() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = r.f.Run(ctx)
		close(done)
	}()
	r.stop = sync.OnceFunc(func() {
		cancel()
		<-done
	})
	r.t.Cleanup(r.stop)
}

func (r *rig) wait(n int) []string {
	r.t.Helper()
	for deadline := time.Now().Add(2 * time.Second); len(r.hook.got()) < n; {
		if time.Now().After(deadline) {
			r.t.Fatalf("got %v, want %d events", r.hook.got(), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
	return r.hook.got()
}

func newFinding(element uuid.UUID, sev evaluation.Severity) evaluation.Finding {
	return evaluation.Finding{ID: uuid.New(), ElementID: element, Severity: sev, Code: "c", RaisedAt: at}
}

func TestForwardFilters(t *testing.T) {
	r := newRig(t)
	r.target(true, func(t *Target) { t.ScopeElementID = r.device; t.MinSeverity = evaluation.SeverityWarning })
	r.run()
	inScope := newFinding(r.port, evaluation.SeverityError)
	r.core.events <- core.FindingRaised{Finding: newFinding(r.port, evaluation.SeverityInfo)}   // zu leicht
	r.core.events <- core.FindingRaised{Finding: newFinding(r.other, evaluation.SeverityError)} // außerhalb
	r.core.events <- core.FindingAcked{Finding: inScope}                                        // nicht abonniert
	r.core.events <- core.FindingRaised{Finding: inScope}
	r.core.events <- core.RunChanged{Run: action.Run{ID: uuid.New(), ElementID: r.port, Status: action.StatusSent}}            // nicht beendet
	r.core.events <- core.RunChanged{Run: action.Run{ID: uuid.New(), ElementID: r.port, ParentID: uuid.New(), FinishedAt: at}} // Kind
	r.core.events <- core.RunChanged{Run: action.Run{ID: uuid.New(), ElementID: r.port, Status: action.StatusConfirmed, FinishedAt: at}}
	r.wait(2)
	// Das Element ist gelöscht: Das Ende geht trotzdem an das Ziel, weil sein raised dorthin ging.
	r.core.view.Store(&core.View{Graph: inventory.New(nil, nil, slog.New(slog.DiscardHandler)).Graph()})
	r.core.events <- core.FindingEnded{Finding: inScope, Reason: evaluation.ReasonDeleted}
	got := r.wait(3)
	time.Sleep(20 * time.Millisecond)
	want := []string{EventFindingRaised, EventActionFinished, EventFindingWithdrawn}
	if len(r.hook.got()) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("events = %v, want %v", r.hook.got(), want)
	}
}

func TestForwardRetriesAndResults(t *testing.T) {
	r := newRig(t)
	target := r.target(true)
	r.hook.fail = 2 // zwei Wiederholungen nötig
	r.run()
	r.core.events <- core.FindingRaised{Finding: newFinding(r.port, evaluation.SeverityError)}
	r.wait(1)
	r.hook.mu.Lock()
	r.hook.fail = 10 // scheitert endgültig
	r.hook.mu.Unlock()
	r.core.events <- core.FindingEnded{Finding: newFinding(r.port, evaluation.SeverityError), Reason: evaluation.ReasonResolved}
	time.Sleep(30 * time.Millisecond)
	r.stop() // schreibt die Ergebnisse
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if len(r.store.results) != 1 || r.store.results[0].TargetID != target.ID || r.store.results[0].Error == "" {
		t.Errorf("results = %+v", r.store.results)
	}
}

func TestForwardTargetChanges(t *testing.T) {
	r := newRig(t)
	r.run()
	target := r.target(false)
	if err := r.f.CreateTarget(t.Context(), uuid.New(), target); err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	r.core.events <- core.FindingRaised{Finding: newFinding(r.port, evaluation.SeverityError)}
	r.wait(1)
	target.Enabled = false
	if err := r.f.UpdateTarget(t.Context(), uuid.New(), target); err != nil {
		t.Fatalf("UpdateTarget: %v", err)
	}
	r.core.events <- core.FindingRaised{Finding: newFinding(r.port, evaluation.SeverityError)}
	if err := r.f.DeleteTarget(t.Context(), uuid.New(), target.ID); err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(r.hook.got()); n != 1 {
		t.Errorf("events after disabling = %d", n)
	}
	bad := target
	bad.ID, bad.Config = uuid.New(), json.RawMessage(`{"url":"ftp://x"}`)
	if err := r.f.CreateTarget(t.Context(), uuid.New(), bad); err == nil {
		t.Error("invalid target accepted")
	}
}
