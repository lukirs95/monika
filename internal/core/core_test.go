package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// invStore ist die Attrappe des Inventar-Stores: ApplyDiscovery bildet FinishDiscovery über den
// Graph nach und kann scheitern.
type invStore struct {
	inventory.Store
	graph func() *inventory.Graph
	mu    sync.Mutex
	err   error
}

func (s *invStore) ApplyDiscovery(_ context.Context, d inventory.Discovery) ([]uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range d.Seen {
		seen[id] = true
	}
	var missing []uuid.UUID
	for id, e := range s.graph().All() {
		if e.SourceID == d.SourceID && e.Present && e.Kind != inventory.KindAccess && !seen[id] {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

func (s *invStore) CreateDevice(context.Context, uuid.UUID, inventory.Element) error { return nil }
func (s *invStore) CreateSource(context.Context, uuid.UUID, inventory.Source, uuid.UUID) error {
	return nil
}
func (s *invStore) UpdateSource(context.Context, uuid.UUID, inventory.Source) error { return nil }
func (s *invStore) DeleteSource(context.Context, uuid.UUID, uuid.UUID) error        { return nil }
func (s *invStore) CreateGroup(context.Context, uuid.UUID, inventory.Element) error { return nil }
func (s *invStore) AddGroupMember(context.Context, uuid.UUID, inventory.Link) error { return nil }

func (s *invStore) setErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// evalStore ist die Attrappe des Evaluation-Stores; alles gelingt, SaveHealth wird gezählt.
type evalStore struct {
	evaluation.Store
	saves *atomic.Int32
}

func (evalStore) CreateRule(context.Context, uuid.UUID, evaluation.Rule) error { return nil }
func (evalStore) AckFinding(context.Context, uuid.UUID, uuid.UUID) error       { return nil }

func (evalStore) RaiseFinding(context.Context, evaluation.Finding) error              { return nil }
func (evalStore) EndFinding(context.Context, uuid.UUID, evaluation.ClearReason) error { return nil }
func (evalStore) SetFindingCause(context.Context, uuid.UUID, uuid.UUID) error         { return nil }
func (s evalStore) SaveHealth(context.Context, []evaluation.ElementHealth) error {
	s.saves.Add(1)
	return nil
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

// rig ist ein laufender Core mit einem Gerät, einer aktivierten Quelle und der Regel temp lte 80.
type rig struct {
	t                      *testing.T
	c                      *Core
	store                  *invStore
	events                 <-chan Event
	logs                   *syncBuffer
	sup                    *fakeSupervisor
	secrets                *fakeSecrets
	pinger                 *fakePinger
	saves                  *atomic.Int32
	stop                   func() // beendet Run und wartet darauf
	device, source, access uuid.UUID
}

// fakeSupervisor zeichnet Aufträge auf.
type fakeSupervisor struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeSupervisor) Start(spec SourceSpec) { f.record("start " + spec.Driver) }
func (f *fakeSupervisor) Stop(uuid.UUID)        { f.record("stop") }

func (f *fakeSupervisor) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeSupervisor) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type fakeSecrets struct{ replaced atomic.Int32 }

func (f *fakeSecrets) Replace(context.Context, uuid.UUID, uuid.UUID, json.RawMessage) error {
	f.replaced.Add(1)
	return nil
}

type fakePinger struct{ err atomic.Pointer[error] }

func (f *fakePinger) Ping(context.Context) error {
	if err := f.err.Load(); err != nil {
		return *err
	}
	return nil
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, device: uuid.New(), source: uuid.New(), access: uuid.New(), logs: &syncBuffer{},
		sup: &fakeSupervisor{}, secrets: &fakeSecrets{}, pinger: &fakePinger{}, saves: &atomic.Int32{}}
	log := slog.New(slog.NewTextHandler(r.logs, nil))
	drivers := func(name string) (sdk.Driver, bool) { return sdk.Driver{Name: name}, name == "videoxlink" }
	r.store = &invStore{}
	inv := inventory.New(r.store, drivers, log)
	r.store.graph = inv.Graph
	inv.Load(inventory.Snapshot{
		Sources: []inventory.Source{{ID: r.source, DeviceID: r.device, Driver: "videoxlink", Enabled: true, AccessElementID: r.access}},
		Elements: []inventory.Element{
			{ID: r.device, Kind: inventory.KindDevice, DeviceID: r.device, Label: "X8", Present: true, Monitored: true},
			{ID: r.access, Kind: inventory.KindAccess, ParentID: r.device, DeviceID: r.device, SourceID: r.source, Present: true, Monitored: true},
		},
	})
	types := func(key string) (vocab.Type, bool) { return vocab.Float, key == "temp" }
	ev := evaluation.New(evalStore{saves: r.saves}, types, func(string) bool { return true }, log)
	ev.Load([]evaluation.Rule{{ID: uuid.New(), Name: "Temperatur", Enabled: true, Severity: evaluation.SeverityWarning,
		Fact: "temp", Op: evaluation.OpLte, Value: []byte(`80`)}}, nil, nil)

	r.c = New(Config{Inventory: inv, Evaluator: ev, Supervisor: r.sup, Secrets: r.secrets, DB: r.pinger, Log: log})
	r.c.tick = 20 * time.Millisecond
	r.c.ping = 20 * time.Millisecond
	var cancel func()
	r.events, cancel = r.c.Subscribe(1024)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- r.c.Run(ctx) }()
	var once sync.Once
	r.stop = func() {
		once.Do(func() {
			stop()
			if err := <-done; err != nil {
				t.Errorf("Run: %v", err)
			}
			cancel()
		})
	}
	t.Cleanup(r.stop)
	return r
}

// waitView wartet, bis eine veröffentlichte View die Bedingung erfüllt.
func (r *rig) waitView(what string, ok func(*View) bool) *View {
	r.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if v := r.c.View(); ok(v) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("timeout waiting for %s", what)
	return nil
}

// collect liest Ereignisse, bis die Bedingung für eines erfüllt ist, und liefert alle gelesenen.
func (r *rig) collect(what string, ok func(Event) bool) []Event {
	r.t.Helper()
	var got []Event
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e := <-r.events:
			got = append(got, e)
			if ok(e) {
				return got
			}
		case <-timeout:
			r.t.Fatalf("timeout waiting for %s, got %d events", what, len(got))
		}
	}
}

func snapshot(temp float64) sdk.Snapshot {
	return sdk.Snapshot{
		Elements: []sdk.Element{{ExternalID: "sdi/1", Kind: vocab.ElementPort, Transport: "sdi", Name: "SDI 1"}},
		Facts:    map[string]sdk.Facts{"sdi/1": {"temp": temp}},
	}
}

// goLive startet die Quelle und meldet Connected und einen Snapshot.
func (r *rig) goLive(rep sdk.Reporter, temp float64) uuid.UUID {
	r.t.Helper()
	r.c.Post(SourceStarted{SourceID: r.source})
	rep.Connected()
	rep.Snapshot(snapshot(temp))
	v := r.waitView("source live", func(v *View) bool { return v.Sources[r.source].State == StateLive })
	port, ok := r.c.inv.ByExternalID(r.source, "sdi/1")
	if !ok || v.Facts[port]["temp"] != temp {
		r.t.Fatalf("port %v, facts %v", ok, v.Facts[port])
	}
	return port
}

func TestLoopSnapshotAndUpdate(t *testing.T) {
	r := newRig(t)
	rep := r.c.Reporter(r.source)
	port := r.goLive(rep, 70)

	added := r.collect("elements changed", func(e Event) bool { _, ok := e.(ElementsChanged); return ok })
	if ec := added[len(added)-1].(ElementsChanged); len(ec.Added) != 1 || ec.Added[0] != port || ec.Version() == 0 {
		t.Errorf("ElementsChanged = %+v", ec)
	}
	v := r.waitView("port ok", func(v *View) bool { return v.Health[port].Health == evaluation.HealthOK })
	if v.Health[r.device].Health != evaluation.HealthOK {
		t.Errorf("device health = %q", v.Health[r.device].Health)
	}

	rep.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/1": {"temp": 85.0}}})
	r.collect("finding raised", func(e Event) bool { _, ok := e.(FindingRaised); return ok })
	v = r.waitView("port warning", func(v *View) bool { return v.Health[port].Health == evaluation.HealthWarning })
	if len(v.Findings) != 1 || v.Facts[port]["temp"] != 85.0 {
		t.Errorf("findings %d, facts %v", len(v.Findings), v.Facts[port])
	}

	// Unbekannte external_id: einmal geloggt, nichts übernommen.
	rep.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/9": {"temp": 1.0}}})
	rep.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/1": {"temp": 70.0}}})
	r.collect("finding ended", func(e Event) bool { _, ok := e.(FindingEnded); return ok })
	if got := bytes.Count([]byte(r.logs.String()), []byte("external_id=sdi/9")); got != 1 {
		t.Errorf("unknown external id logged %d times", got)
	}
}

func TestLoopUpdateBeforeSnapshotDropped(t *testing.T) {
	r := newRig(t)
	old := r.c.Reporter(r.source)
	port := r.goLive(old, 70)

	// Neue Instanz: Bis zu ihrem Snapshot zählt kein Update, und die alte Instanz zählt nicht mehr.
	rep := r.c.Reporter(r.source)
	rep.Connected()
	rep.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/1": {"temp": 60.0}}})
	old.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/1": {"temp": 50.0}}})
	rep.Snapshot(snapshot(75))
	events := r.collect("snapshot of the new instance", func(e Event) bool {
		fc, ok := e.(FactsChanged)
		return ok && fc.Facts[port]["temp"] == 75.0
	})
	for _, e := range events {
		if fc, ok := e.(FactsChanged); ok && (fc.Facts[port]["temp"] == 60.0 || fc.Facts[port]["temp"] == 50.0) {
			t.Errorf("dropped update applied: %v", fc.Facts[port])
		}
	}
}

func TestLoopSnapshotRetry(t *testing.T) {
	r := newRig(t)
	r.store.setErr(errors.New("db down"))
	rep := r.c.Reporter(r.source)
	r.c.Post(SourceStarted{SourceID: r.source})
	rep.Connected()
	rep.Snapshot(snapshot(70))
	raised := r.collect("discovery failed", func(e Event) bool { _, ok := e.(FindingRaised); return ok })
	if f := raised[len(raised)-1].(FindingRaised).Finding; f.Code != evaluation.CodeDiscoveryFailed || f.ElementID != r.access {
		t.Fatalf("finding = %+v", f)
	}
	if st := r.c.View().Sources[r.source].State; st == StateLive {
		t.Fatal("live although the snapshot was not saved")
	}
	// Ein Update wird in den gemerkten Snapshot gemischt.
	rep.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/1": {"temp": 72.0}}})
	r.store.setErr(nil)
	r.collect("discovery failed resolved", func(e Event) bool { _, ok := e.(FindingEnded); return ok })
	v := r.waitView("live after retry", func(v *View) bool { return v.Sources[r.source].State == StateLive })
	port, _ := r.c.inv.ByExternalID(r.source, "sdi/1")
	if v.Facts[port]["temp"] != 72.0 {
		t.Errorf("facts = %v, want merged update", v.Facts[port])
	}
}

func TestLoopMessages(t *testing.T) {
	r := newRig(t)
	run := action.Run{ID: uuid.New(), Status: action.StatusSent}
	r.c.Post(RunChanged{Run: run})
	r.waitView("run", func(v *View) bool { return v.Runs[run.ID].Status == action.StatusSent })
	run.Status = action.StatusConfirmed
	r.c.Post(RunChanged{Run: run})
	r.waitView("run gone", func(v *View) bool { _, ok := v.Runs[run.ID]; return !ok })

	r.c.Post(DBPing{Err: errors.New("timeout")})
	r.waitView("db down", func(v *View) bool { return !v.DB.OK && v.DB.Error == "timeout" })
	r.c.Post(DBPing{OK: true})
	r.waitView("db up", func(v *View) bool { return v.DB.OK })

	r.c.Post(SourceFailed{SourceID: r.source, Err: errors.New("refused")})
	r.waitView("backoff", func(v *View) bool {
		return v.Sources[r.source].State == StateBackoff && v.Sources[r.source].Error == "refused"
	})
	r.c.Post(InfluxStatus{OK: true, Buffered: 3})
	r.waitView("history", func(v *View) bool { return v.History.Buffered == 3 })
}

func TestMailboxCoalesces(t *testing.T) {
	c := &Core{now: time.Now, mailboxes: map[uuid.UUID]*mailbox{}, signal: make(chan struct{}, 1)}
	id := uuid.New()
	m := c.Reporter(id).(*mailbox)
	m.Update(sdk.Update{Facts: map[string]sdk.Facts{"a": {"x": 1.0, "y": 1.0}}})
	m.Update(sdk.Update{Facts: map[string]sdk.Facts{"a": {"x": 2.0}, "b": {"z": 1.0}}, Alarms: map[string][]sdk.Alarm{"a": {{Code: "los"}}}})
	m.Heartbeat()
	m.Snapshot(sdk.Snapshot{})
	m.Update(sdk.Update{Facts: map[string]sdk.Facts{"a": {"x": 3.0}}})
	m.Disconnected(nil)
	m.Heartbeat()
	m.Heartbeat()

	ready := c.takeReady()
	if len(ready) != 1 {
		t.Fatalf("ready = %d, want the mailbox once", len(ready))
	}
	items := ready[0].take()
	kinds := []itemKind{itemUpdate, itemSnapshot, itemUpdate, itemDisconnected, itemHeartbeat}
	if len(items) != len(kinds) {
		t.Fatalf("items = %d, want %d", len(items), len(kinds))
	}
	for i, k := range kinds {
		if items[i].kind != k {
			t.Errorf("item %d kind = %d, want %d", i, items[i].kind, k)
		}
	}
	u := items[0].upd
	if u.Facts["a"]["x"] != 2.0 || u.Facts["a"]["y"] != 1.0 || u.Facts["b"]["z"] != 1.0 || len(u.Alarms["a"]) != 1 {
		t.Errorf("merged update = %+v", u)
	}
	if items[2].upd.Facts["a"]["x"] != 3.0 {
		t.Error("update after snapshot merged across it")
	}
}
