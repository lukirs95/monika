package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/coretest"
	"github.com/lukirs95/monika/internal/core/evaluation"
)

// Benchmarks der API in Stufen bis 1000 Geräte (je 100 Elemente, eine Gruppe pro 10 Geräte):
// boss sieht alles, anna nur die erste Gruppe (10 Geräte).

func benchServer(b *testing.B, devices int) (*Server, *coretest.World) {
	b.Helper()
	w := coretest.NewWorld(devices)
	s := newServerWith(b, &fakeCore{})
	s.cfg.Core.(*fakeCore).view.Store(w.View)
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{
		boss.UserID: {{Permission: auth.PermView}},
		anna.UserID: {{Permission: auth.PermView, ScopeElementID: w.Groups[0]}},
	}
	return s, w
}

// get schickt eine Anfrage durch den ganzen Handler (Guard, Prüfung, Handler, JSON).
func get(b *testing.B, h http.Handler, path, cookie string) int {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: cookie})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		b.Fatalf("GET %s = %d %s", path, w.Code, w.Body)
	}
	return w.Body.Len()
}

func BenchmarkState(b *testing.B) {
	for _, n := range coretest.Sizes {
		s, _ := benchServer(b, n)
		h := s.Handler()
		for _, v := range []struct{ name, cookie string }{{"global", "boss"}, {"group", "anna"}} {
			b.Run(fmt.Sprintf("devices=%d/view=%s", n, v.name), func(b *testing.B) {
				var size int
				for b.Loop() {
					size = get(b, h, "/api/v1/state", v.cookie)
				}
				b.ReportMetric(float64(size)/1024, "KiB")
			})
		}
	}
}

// BenchmarkRequest ist eine kleine Anfrage: was Guard (Route, Anmeldung, Prüfung gegen die
// Spezifikation) und Handler pro Aufruf kosten.
func BenchmarkRequest(b *testing.B) {
	for _, n := range []int{10, 1000} {
		s, w := benchServer(b, n)
		h := s.Handler()
		path := "/api/v1/elements/" + w.Devices[0].Ports[0].String() + "/facts"
		b.Run(fmt.Sprintf("devices=%d", n), func(b *testing.B) {
			for b.Loop() {
				get(b, h, path, "anna")
			}
		})
	}
}

// BenchmarkWatch beobachtet ein Gerät (100 Elemente) und liefert dessen Fakten.
func BenchmarkWatch(b *testing.B) {
	for _, n := range coretest.Sizes {
		s, w := benchServer(b, n)
		s.dist.running = true
		st, _, _ := s.dist.open(boss, "")
		h := s.Handler()
		b.Run(fmt.Sprintf("devices=%d", n), func(b *testing.B) {
			for b.Loop() {
				st.mu.Lock()
				st.scopes, st.watched = map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
				st.mu.Unlock()
				r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/events/"+st.id.String()+"/watch",
					strings.NewReader(`{"add":["`+w.Devices[0].ID.String()+`"]}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "https://monika.example")
				r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "boss"})
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, r)
				if rec.Code != http.StatusOK {
					b.Fatalf("watch = %d %s", rec.Code, rec.Body)
				}
			}
		})
	}
}

// BenchmarkDistribute verteilt ein HealthChanged über 100 Elemente an viele Ströme.
func BenchmarkDistribute(b *testing.B) {
	w := coretest.NewWorld(10)
	ev := core.HealthChanged{}
	for _, id := range w.Devices[0].Ports {
		ev.Health = append(ev.Health, evaluation.ElementHealth{ElementID: id})
	}
	for _, streams := range []int{1, 10, 50, 100, 200} {
		b.Run(fmt.Sprintf("streams=%d", streams), func(b *testing.B) {
			d := NewDistributor(&fakeSource{}, func() uint64 { return 0 })
			d.running = true
			var all []*stream
			for range streams {
				st, _, _ := d.open(anna, "")
				all = append(all, st)
			}
			v := uint64(0)
			for b.Loop() {
				v++
				d.dispatch(core.WithVersion(ev, v))
				for _, st := range all {
					st.take()
				}
			}
		})
	}
}

// discard ist ein ResponseWriter ohne Ziel, für den Schreibweg eines Stroms.
type discard struct{ h http.Header }

func (d *discard) Header() http.Header         { return d.h }
func (d *discard) Write(p []byte) (int, error) { return len(p), nil }
func (d *discard) WriteHeader(int)             {}

// BenchmarkStreamWrite misst, was ein Strom pro Eintrag beim Schreiben kostet: Nutzdaten aus der
// View, gefiltert nach den Rechten. elements_changed vergleicht die ganze Sicht (V4).
func BenchmarkStreamWrite(b *testing.B) {
	for _, n := range coretest.Sizes {
		s, w := benchServer(b, n)
		port := w.Devices[0].Ports[0]
		items := map[string]*item{
			"elements": {key: key{kind: kindElements}, version: 1, ids: map[uuid.UUID]bool{port: true}},
			"health":   {key: key{kind: kindHealth}, version: 1, ids: map[uuid.UUID]bool{}},
			"facts":    {key: key{kindFacts, port}, version: 1, facts: map[string]bool{"sdi.crc_errors": true}},
		}
		for _, id := range w.Devices[0].Ports {
			items["health"].ids[id] = true
		}
		for view, who := range map[string]auth.Principal{"global": boss, "group": anna} {
			for _, name := range []string{"elements", "health", "facts"} {
				b.Run(fmt.Sprintf("devices=%d/view=%s/%s", n, view, name), func(b *testing.B) {
					st := &stream{id: uuid.New(), principal: who, queue: newQueue(), closed: make(chan struct{}),
						scopes: map[uuid.UUID]bool{w.Devices[0].ID: true}, watched: map[uuid.UUID]bool{}}
					es := &eventStream{s: s, ctx: context.Background(), st: st, w: &discard{h: http.Header{}}}
					es.rc = http.NewResponseController(es.w)
					es.reset(w.View)
					for b.Loop() {
						if name == "elements" { // ein neuer Graph: Sicht neu berechnen, dann vergleichen
							es.sight = s.sight(es.view, who)
						}
						if err := es.item(items[name]); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
