package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Benchmarks des Core in Stufen. Pro Gerät eine Quelle mit 100 Elementen (Zugang, 3 Module, 96
// Ports), 3 Fakten pro Port, 48 feeds-Links; eine Gruppe pro 10 Geräte und eine über allen;
// immer 200 Regeln; etwa jeder 50. Port verletzt Regeln.

var benchSizes = []int{10, 50, 100, 200, 400, 1000}

const (
	benchModules = 3
	benchPorts   = 32 // pro Modul
	benchRules   = 200
	benchDriver  = "bench"
)

type benchDevice struct {
	id, source, access uuid.UUID
	ports              []uuid.UUID
	portExt            []string
	snap               sdk.Snapshot
}

type benchWorld struct {
	c       *Core
	devices []benchDevice
}

// nopInvStore: ApplyDiscovery gelingt ohne Arbeit, damit der Benchmark den Core misst.
type nopInvStore struct{ inventory.Store }

func (nopInvStore) ApplyDiscovery(context.Context, inventory.Discovery) ([]uuid.UUID, error) {
	return nil, nil
}

func benchTypes(key string) (vocab.Type, bool) {
	t, ok := map[string]vocab.Type{"temp": vocab.Float, "signal": vocab.Bool, "crc": vocab.Int, vocab.Present: vocab.Bool}[key]
	return t, ok
}

// benchTemp ist für jede Runde ein neuer Wert unterhalb aller Grenzen, damit jede Meldung eine
// echte Änderung ist.
func benchTemp(i int) float64 { return 40 + float64(i%100000)/10000 }

func portFacts(temp float64) sdk.Facts {
	return sdk.Facts{"temp": temp, "signal": true, "crc": int64(0)}
}

// newBenchWorld baut die Welt; live = true macht alle Quellen direkt Live (ohne laufende
// Schleife) und bewertet einmal alles.
func newBenchWorld(b *testing.B, devices int, live bool) *benchWorld {
	b.Helper()
	log := slog.New(slog.DiscardHandler)
	drivers := func(name string) (sdk.Driver, bool) { return sdk.Driver{Name: name}, name == benchDriver }
	w := &benchWorld{}
	var snap inventory.Snapshot
	var groups []uuid.UUID
	top := uuid.New()
	snap.Elements = append(snap.Elements, inventory.Element{ID: top, Kind: inventory.KindGroup, Label: "all", Present: true, Monitored: true})
	for d := range devices {
		dev := benchDevice{id: uuid.New(), source: uuid.New(), access: uuid.New()}
		if d%10 == 0 {
			g := uuid.New()
			groups = append(groups, g)
			snap.Elements = append(snap.Elements, inventory.Element{ID: g, Kind: inventory.KindGroup, Present: true, Monitored: true})
			snap.Links = append(snap.Links, inventory.Link{ID: uuid.New(), Type: inventory.LinkMember, FromID: top, ToID: g})
		}
		snap.Links = append(snap.Links, inventory.Link{ID: uuid.New(), Type: inventory.LinkMember, FromID: groups[len(groups)-1], ToID: dev.id})
		snap.Sources = append(snap.Sources, inventory.Source{ID: dev.source, DeviceID: dev.id, Driver: benchDriver, Enabled: true, AccessElementID: dev.access})
		el := func(id, parent uuid.UUID, kind inventory.Kind, ext string) inventory.Element {
			return inventory.Element{ID: id, Kind: kind, ParentID: parent, DeviceID: dev.id, SourceID: dev.source, ExternalID: ext, Name: ext, Present: true, Monitored: true}
		}
		snap.Elements = append(snap.Elements,
			inventory.Element{ID: dev.id, Kind: inventory.KindDevice, DeviceID: dev.id, Label: fmt.Sprintf("D%d", d), Present: true, Monitored: true},
			el(dev.access, dev.id, inventory.KindAccess, ""))
		dev.snap.Facts = map[string]sdk.Facts{}
		for m := range benchModules {
			mid, mext := uuid.New(), fmt.Sprintf("m/%d", m)
			snap.Elements = append(snap.Elements, el(mid, dev.access, vocab.ElementModule, mext))
			dev.snap.Elements = append(dev.snap.Elements, sdk.Element{ExternalID: mext, Kind: vocab.ElementModule, Name: mext})
			for p := range benchPorts {
				pid, pext := uuid.New(), fmt.Sprintf("p/%d/%d", m, p)
				snap.Elements = append(snap.Elements, el(pid, mid, vocab.ElementPort, pext))
				dev.snap.Elements = append(dev.snap.Elements, sdk.Element{ExternalID: pext, ParentID: mext, Kind: vocab.ElementPort, Name: pext})
				temp := 50.0
				if (len(dev.ports)+d)%50 == 0 {
					temp = 95 // verletzt
				}
				dev.snap.Facts[pext] = portFacts(temp)
				if p%2 == 1 {
					prev := dev.ports[len(dev.ports)-1]
					snap.Links = append(snap.Links, inventory.Link{ID: uuid.New(), Type: inventory.LinkFeeds, SourceID: dev.source, FromID: prev, ToID: pid})
					dev.snap.Links = append(dev.snap.Links, sdk.Link{Type: sdk.LinkFeeds, From: sdk.Endpoint{ExternalID: dev.portExt[len(dev.portExt)-1]}, To: sdk.Endpoint{ExternalID: pext}})
				}
				dev.ports, dev.portExt = append(dev.ports, pid), append(dev.portExt, pext)
			}
		}
		w.devices = append(w.devices, dev)
	}

	inv := inventory.New(nopInvStore{}, drivers, log)
	inv.Load(snap)
	ev := evaluation.New(evalStore{saves: &atomic.Int32{}}, benchTypes, func(string) bool { return true }, log)
	ev.Load(benchRuleSet(w.devices, groups), nil, nil)
	w.c = New(Config{Inventory: inv, Evaluator: ev, Log: log})
	if live {
		w.goLiveDirect(b)
	}
	return w
}

// benchRuleSet: 4 globale Regeln, eine pro Gruppe, der Rest an den Geräten (reihum).
func benchRuleSet(devices []benchDevice, groups []uuid.UUID) []evaluation.Rule {
	rule := func(scope uuid.UUID, fact string, op evaluation.Op, value string, sev evaluation.Severity) evaluation.Rule {
		return evaluation.Rule{ID: uuid.New(), Name: fact + string(op) + value, Enabled: true, Severity: sev, ScopeElementID: scope, Fact: fact, Op: op, Value: []byte(value)}
	}
	rules := []evaluation.Rule{
		rule(uuid.Nil, "temp", evaluation.OpLte, "80", evaluation.SeverityWarning),
		rule(uuid.Nil, "temp", evaluation.OpLte, "90", evaluation.SeverityError),
		rule(uuid.Nil, "signal", evaluation.OpEq, "true", evaluation.SeverityError),
		rule(uuid.Nil, "crc", evaluation.OpEq, "0", evaluation.SeverityWarning),
	}
	for _, g := range groups {
		rules = append(rules, rule(g, "temp", evaluation.OpLte, "85", evaluation.SeverityWarning))
	}
	for i := 0; len(rules) < benchRules; i++ {
		rules = append(rules, rule(devices[i%len(devices)].id, "temp", evaluation.OpLte, fmt.Sprint(70+i%10), evaluation.SeverityWarning))
	}
	return rules
}

// goLiveDirect setzt Fakten und Lebenszeichen ohne laufende Schleife und bewertet alles.
func (w *benchWorld) goLiveDirect(b *testing.B) {
	c := w.c
	now := time.Now()
	ctx := context.Background()
	for _, d := range w.devices {
		c.live.Started(d.source, 0, now)
		c.live.Connected(d.source, now)
		c.live.SnapshotApplied(d.source, now)
		facts, _ := c.inv.FactsByID(d.source, d.snap.Facts)
		c.facts.Replace(d.source, facts)
	}
	r := c.newRound()
	var all []uuid.UUID
	for id := range c.inv.Graph().All() {
		all = append(all, id)
	}
	changes, err := c.ev.Evaluate(ctx, now, all, c.state(now))
	c.applyFindings(r, changes, err)
	r.facts = true
	c.publish(ctx, r)
	if len(c.View().Findings) == 0 {
		b.Fatal("no background findings")
	}
}

func eachSize(b *testing.B, run func(b *testing.B, devices int)) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("devices=%d", n), func(b *testing.B) { run(b, n) })
	}
}

// BenchmarkUpdate: eine Meldung mit 10 geänderten Fakten bis zur veröffentlichten View, ohne
// Bündelung (eine Runde pro Meldung).
func BenchmarkUpdate(b *testing.B) {
	eachSize(b, func(b *testing.B, n int) {
		w := newBenchWorld(b, n, true)
		ctx := context.Background()
		i := 0
		for b.Loop() {
			d := w.devices[i%len(w.devices)]
			u := sdk.Update{Facts: map[string]sdk.Facts{}}
			for k := range 10 {
				u.Facts[d.portExt[k*9]] = sdk.Facts{"crc": int64(0), "signal": true, "temp": benchTemp(i)}
			}
			r := w.c.newRound()
			w.c.applyUpdate(ctx, r, d.source, u)
			w.c.publish(ctx, r)
			i++
		}
		b.ReportMetric(float64(len(w.c.View().Findings)), "findings")
	})
}

// BenchmarkFindingToggle: Ein Port wechselt zwischen verletzt und erfüllt; Befunde, Ursache und
// Symptom, Health mit Rollup und View.
func BenchmarkFindingToggle(b *testing.B) {
	eachSize(b, func(b *testing.B, n int) {
		w := newBenchWorld(b, n, true)
		ctx := context.Background()
		d := w.devices[len(w.devices)/2]
		ext := d.portExt[1] // wird von Port 0 gespeist
		i := 0
		for b.Loop() {
			temp := 50.0
			if i%2 == 0 {
				temp = 95
			}
			r := w.c.newRound()
			w.c.applyUpdate(ctx, r, d.source, sdk.Update{Facts: map[string]sdk.Facts{ext: portFacts(temp)}})
			w.c.publish(ctx, r)
			i++
		}
	})
}

// BenchmarkApplySnapshot: vollständiger Stand einer Quelle (100 Elemente), unverändert bzw. mit
// geänderter Struktur.
func BenchmarkApplySnapshot(b *testing.B) {
	for _, changed := range []bool{false, true} {
		b.Run(fmt.Sprintf("changed=%v", changed), func(b *testing.B) {
			eachSize(b, func(b *testing.B, n int) {
				w := newBenchWorld(b, n, false)
				ctx := context.Background()
				d := w.devices[0]
				if _, err := w.c.inv.ApplySnapshot(ctx, d.source, d.snap); err != nil {
					b.Fatal(err)
				}
				i := 0
				for b.Loop() {
					s := d.snap
					if changed {
						s.Elements = append([]sdk.Element(nil), d.snap.Elements...)
						s.Elements[0].Name = fmt.Sprint(i) // Struktur ändert sich
					}
					if _, err := w.c.inv.ApplySnapshot(ctx, d.source, s); err != nil {
						b.Fatal(err)
					}
					i++
				}
			})
		})
	}
}

// BenchmarkGraph: Neubau des Graphs nach einer Änderung am Inventar.
func BenchmarkGraph(b *testing.B) {
	eachSize(b, func(b *testing.B, n int) {
		w := newBenchWorld(b, n, false)
		for b.Loop() {
			w.c.inv.Load(inventory.Snapshot{}) // macht den Graph ungültig, ändert nichts
			w.c.inv.Graph()
		}
	})
}

// BenchmarkLoop: die laufende Schleife mit 100 Meldungen verschiedener Geräte hintereinander;
// gemessen bis die letzte verarbeitet ist (Bündelung pro Runde).
func BenchmarkLoop(b *testing.B) {
	eachSize(b, func(b *testing.B, n int) {
		w := newBenchWorld(b, n, false)
		w.c.tick = time.Hour
		events, cancel := w.c.Subscribe(1 << 20)
		defer cancel()
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			_ = w.c.Run(ctx)
			close(done)
		}()
		defer func() {
			stop()
			<-done
		}()
		reps := make([]sdk.Reporter, len(w.devices))
		for i, d := range w.devices {
			reps[i] = w.c.Reporter(d.source)
			w.c.Post(SourceStarted{SourceID: d.source})
			reps[i].Connected()
			reps[i].Snapshot(d.snap)
		}
		waitLive(b, w)
		drain(events)

		const burst = 100
		round := 0
		for b.Loop() {
			round++
			for k := range burst {
				i := (round*burst + k) % len(w.devices)
				reps[i].Update(sdk.Update{Facts: map[string]sdk.Facts{w.devices[i].portExt[3]: portFacts(benchTemp(round))}})
			}
			waitFacts(b, w, events, round)
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*burst), "ns/update")
	})
}

func waitLive(b *testing.B, w *benchWorld) {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		v := w.c.View()
		live := 0
		for _, st := range v.Sources {
			if st.State == StateLive {
				live++
			}
		}
		if live == len(w.devices) {
			return
		}
		if time.Now().After(deadline) {
			b.Fatalf("only %d of %d sources live", live, len(w.devices))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func drain(events <-chan Event) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

// waitFacts wartet, bis die Updates der Runde verarbeitet sind: alle 100 geänderten Ports haben
// den neuen Wert (gezählt über FactsChanged).
func waitFacts(b *testing.B, w *benchWorld, events <-chan Event, round int) {
	want := benchTemp(round)
	seen := map[uuid.UUID]bool{}
	timeout := time.After(time.Minute)
	for len(seen) < min(100, len(w.devices)) {
		select {
		case e := <-events:
			if fc, ok := e.(FactsChanged); ok {
				for id, f := range fc.Facts {
					if f["temp"] == want {
						seen[id] = true
					}
				}
			}
		case <-timeout:
			b.Fatalf("round %d: %d updates processed", round, len(seen))
		}
	}
}
