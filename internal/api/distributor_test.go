package api

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
)

func at(e core.Event, v uint64) core.Event { return core.WithVersion(e, v) }

func TestQueueCoalesces(t *testing.T) {
	f1, f2, el, run := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	watched := func(id uuid.UUID) bool { return id == el }
	q := newQueue()
	for _, e := range []core.Event{
		at(core.FindingRaised{Finding: evaluation.Finding{ID: f1}}, 1),
		at(core.ElementsChanged{Added: []uuid.UUID{el}}, 2),
		at(core.FindingRaised{Finding: evaluation.Finding{ID: f2}}, 3),
		at(core.FactsChanged{Changed: map[uuid.UUID][]string{el: {"a"}, uuid.New(): {"x"}}}, 3),
		at(core.FindingAcked{Finding: evaluation.Finding{ID: f1}}, 4), // f1 rückt hinter f2
		at(core.ElementsChanged{Deleted: []uuid.UUID{f2}, LinksChanged: true}, 5),
		at(core.FactsChanged{Changed: map[uuid.UUID][]string{el: {"b"}}}, 5),
		at(core.RunChanged{Run: action.Run{ID: run, Status: action.StatusSent}}, 6),
		at(core.RunChanged{Run: action.Run{ID: run, Status: action.StatusConfirmed}}, 7),
	} {
		q.push(e, watched)
	}
	resync, items := q.take()
	var order []key
	for _, it := range items {
		order = append(order, it.key)
	}
	want := []key{{kindFinding, f2}, {kindFinding, f1}, {kind: kindElements}, {kindFacts, el}, {kindRun, run}}
	if resync || !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := 1; i < len(items); i++ {
		if items[i].version < items[i-1].version {
			t.Errorf("versions not ascending: %d after %d", items[i].version, items[i-1].version)
		}
	}
	byKey := map[key]*item{}
	for _, it := range items {
		byKey[it.key] = it
	}
	if f := byKey[key{kindFinding, f1}].finding; !f.raised || !f.acked || f.ended != nil {
		t.Errorf("finding f1 = %+v", f)
	}
	if e := byKey[key{kind: kindElements}]; !e.ids[el] || !e.deleted[f2] || !e.links || e.version != 5 {
		t.Errorf("elements = %+v", e)
	}
	if f := byKey[key{kindFacts, el}]; len(f.facts) != 2 {
		t.Errorf("facts = %v, want a and b; unwatched elements left out", f.facts)
	}
	if r := byKey[key{kindRun, run}]; r.run.Status != action.StatusConfirmed {
		t.Errorf("run = %s, want the newest state", r.run.Status)
	}
	if _, items := q.take(); len(items) != 0 {
		t.Errorf("second take = %d items", len(items))
	}

	// resync verwirft alles; bis abgeholt wurde, kommt nichts mehr hinzu.
	q.push(at(core.FindingRaised{Finding: evaluation.Finding{ID: f1}}, 8), watched)
	q.requestResync()
	q.push(at(core.FindingRaised{Finding: evaluation.Finding{ID: f2}}, 9), watched)
	if resync, items := q.take(); !resync || len(items) != 0 {
		t.Errorf("after resync = %v, %d items", resync, len(items))
	}
}

// fakeSource ist der Core für den Verteiler: Jedes Abo bekommt einen eigenen Kanal.
type fakeSource struct {
	mu      sync.Mutex
	version uint64
	subs    []chan core.Event
}

func (f *fakeSource) View() *core.View {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &core.View{Version: f.version}
}

func (f *fakeSource) Subscribe(int) (<-chan core.Event, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan core.Event, 1024)
	f.subs = append(f.subs, ch)
	return ch, func() {}
}

// emit veröffentlicht ein Ereignis mit der nächsten Version.
func (f *fakeSource) emit(e core.Event) uint64 {
	f.mu.Lock()
	f.version++
	v, ch := f.version, f.subs[len(f.subs)-1]
	f.mu.Unlock()
	ch <- at(e, v)
	return v
}

// drop hängt den Verteiler ab wie der Hub bei vollem Puffer.
func (f *fakeSource) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.subs[len(f.subs)-1])
}

type distRig struct {
	src   *fakeSource
	d     *Distributor
	authz atomic.Uint64
	stop  func()
}

func newDistRig(t *testing.T) *distRig {
	t.Helper()
	r := &distRig{src: &fakeSource{version: 10}}
	r.d = NewDistributor(r.src, r.authz.Load)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- r.d.Run(ctx) }()
	r.stop = sync.OnceFunc(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	t.Cleanup(r.stop)
	waitFor(t, "running", func() bool { _, _, err := r.d.open(anna, ""); return err == nil })
	return r
}

// waitFor wartet, bis ok gilt.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !ok(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
	}
}

// next wartet auf Gesammeltes im Strom.
func next(t *testing.T, st *stream) (bool, []*item) {
	t.Helper()
	select {
	case <-st.signal:
	case <-time.After(3 * time.Second):
		t.Fatal("nothing for the stream")
	}
	return st.take()
}

func versions(items []*item) []uint64 {
	var out []uint64
	for _, it := range items {
		out = append(out, it.version)
	}
	return out
}

func TestDistributorStreams(t *testing.T) {
	r := newDistRig(t)
	st, hello, err := r.d.open(anna, "")
	if err != nil || hello != 10 {
		t.Fatalf("open = %d, %v; want hello at the version of the view", hello, err)
	}
	watchedEl := uuid.New()
	st.watched[watchedEl] = true

	v := r.src.emit(core.FindingRaised{Finding: evaluation.Finding{ID: uuid.New()}})
	if _, items := next(t, st); !slices.Equal(versions(items), []uint64{v}) {
		t.Errorf("first event = %v", versions(items))
	}
	r.src.emit(core.FactsChanged{Changed: map[uuid.UUID][]string{uuid.New(): {"a"}}}) // nicht beobachtet
	v = r.src.emit(core.FactsChanged{Changed: map[uuid.UUID][]string{watchedEl: {"a"}}})
	if _, items := next(t, st); len(items) != 1 || items[0].key != (key{kindFacts, watchedEl}) || items[0].version != v {
		t.Errorf("facts = %+v", items)
	}

	// Wiederaufnahme ab einschließlich Last-Event-ID, ohne Fakten.
	f := uuid.New()
	v1 := r.src.emit(core.FindingRaised{Finding: evaluation.Finding{ID: f}})
	v2 := r.src.emit(core.FindingAcked{Finding: evaluation.Finding{ID: f}})
	v3 := r.src.emit(core.ElementsChanged{Added: []uuid.UUID{uuid.New()}})
	waitFor(t, "dispatched", func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return r.d.version == v3 })
	resumed, hello, _ := r.d.open(anna, strconv.FormatUint(v2, 10))
	if resync, items := resumed.take(); resync || hello != v3 || !slices.Equal(versions(items), []uint64{v2, v3}) {
		t.Errorf("resume from %d (raised at %d) = %v, %v, hello %d", v2, v1, resync, versions(items), hello)
	}
	if resync, _ := (func() (bool, []*item) { s, _, _ := r.d.open(anna, "3"); return s.take() })(); !resync {
		t.Error("Last-Event-ID older than the ring: no resync")
	}
	if resync, _ := (func() (bool, []*item) { s, _, _ := r.d.open(anna, "kaputt"); return s.take() })(); !resync {
		t.Error("broken Last-Event-ID: no resync")
	}

	if _, ok := r.d.stream(st.id); !ok || r.d.Streams() < 2 {
		t.Errorf("streams = %d", r.d.Streams())
	}
	r.d.close(st)
	if _, ok := r.d.stream(st.id); ok {
		t.Error("closed stream still registered")
	}
}

func TestDistributorRingEvicts(t *testing.T) {
	r := newDistRig(t)
	var last uint64
	for range ringSize + 5 {
		last = r.src.emit(core.HealthChanged{})
	}
	waitFor(t, "dispatched", func() bool { r.d.mu.Lock(); defer r.d.mu.Unlock(); return r.d.version == last })
	tooOld, _, _ := r.d.open(anna, strconv.FormatUint(last-ringSize, 10))
	inRing, _, _ := r.d.open(anna, strconv.FormatUint(last-ringSize+1, 10))
	if resync, _ := tooOld.take(); !resync {
		t.Error("evicted version: no resync")
	}
	if resync, items := inRing.take(); resync || len(items) != 1 {
		t.Errorf("oldest version in the ring = %v, %d items", resync, len(items)) // Health ist ein Eintrag
	}
}

func TestDistributorResync(t *testing.T) {
	r := newDistRig(t)
	st, _, _ := r.d.open(anna, "")

	// Abgehängt vom Core: neu anmelden, alle laden neu, der Ring beginnt neu.
	r.src.drop()
	if resync, _ := next(t, st); !resync {
		t.Fatal("dropped by the core: no resync")
	}
	v := r.src.emit(core.HealthChanged{})
	if resync, items := next(t, st); resync || !slices.Equal(versions(items), []uint64{v}) {
		t.Errorf("after resubscribing = %v, %v", resync, versions(items))
	}

	// Rechte geändert.
	r.authz.Add(1)
	if resync, _ := next(t, st); !resync {
		t.Fatal("authz changed: no resync")
	}

	// Ende des Verteilers schließt alle Ströme; danach gibt es keine neuen.
	r.stop()
	select {
	case <-st.closed:
	default:
		t.Error("stream not closed")
	}
	if _, _, err := r.d.open(anna, ""); err == nil {
		t.Error("open after stop")
	}
}
