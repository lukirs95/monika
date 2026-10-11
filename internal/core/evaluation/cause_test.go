package evaluation

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
)

// causeGraph: Gerät mit Zugang A und Port P1; ein zweites Gerät mit Zugang A2 und den Ports
// P2 … P5 sowie G (Gruppe).
//
//	A ─parent→ P1 ─feeds→ P2 ←uses─ P3       P4 ⇄ P5 (feeds im Kreis)
//	A2 ─parent→ P2, P3, P4, P5               G ∋ P1 (member), P4 represents P1
func causeGraph(t *testing.T) (*inventory.Graph, map[string]uuid.UUID) {
	t.Helper()
	ids := map[string]uuid.UUID{}
	for _, n := range []string{"D", "D2", "S", "S2", "A", "A2", "P1", "P2", "P3", "P4", "P5", "G"} {
		ids[n] = uuid.New()
	}
	el := func(name, parent, device, source string) inventory.Element {
		return inventory.Element{ID: ids[name], Kind: "port", ParentID: ids[parent], DeviceID: ids[device], SourceID: ids[source], Present: true, Monitored: true}
	}
	link := func(typ inventory.LinkType, from, to string) inventory.Link {
		return inventory.Link{ID: uuid.New(), Type: typ, FromID: ids[from], ToID: ids[to]}
	}
	inv := inventory.New(nil, sdk.Lookup, slog.New(slog.DiscardHandler))
	inv.Load(inventory.Snapshot{
		Elements: []inventory.Element{
			{ID: ids["D"], Kind: inventory.KindDevice}, {ID: ids["D2"], Kind: inventory.KindDevice},
			el("A", "D", "D", "S"), el("P1", "A", "D", "S"),
			el("A2", "D2", "D2", "S2"), el("P2", "A2", "D2", "S2"), el("P3", "A2", "D2", "S2"),
			el("P4", "A2", "D2", "S2"), el("P5", "A2", "D2", "S2"),
			{ID: ids["G"], Kind: inventory.KindGroup},
		},
		Links: []inventory.Link{
			link(inventory.LinkFeeds, "P1", "P2"), link(inventory.LinkUses, "P3", "P2"),
			link(inventory.LinkFeeds, "P4", "P5"), link(inventory.LinkFeeds, "P5", "P4"),
			link(inventory.LinkMember, "G", "P1"), link(inventory.LinkRepresents, "P4", "P1"),
		},
	})
	return inv.Graph(), ids
}

func TestCauses(t *testing.T) {
	g, ids := causeGraph(t)
	t0 := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	type spec struct {
		name, element string
		severity      Severity
		age           int // Minuten vor t0: größer = älter
	}
	tests := []struct {
		name     string
		findings []spec
		want     map[string]string // Befund → Ursache ("" = selbst Ursache)
	}{
		{"single", []spec{{"p1", "P1", SeverityError, 0}}, map[string]string{"p1": ""}},
		{"parent", []spec{{"a", "A", SeverityError, 0}, {"p1", "P1", SeverityWarning, 1}},
			map[string]string{"a": "", "p1": "a"}},
		{"root over several steps", []spec{{"a", "A", SeverityError, 0}, {"p1", "P1", SeverityError, 0}, {"p2", "P2", SeverityError, 0}},
			map[string]string{"a": "", "p1": "a", "p2": "a"}},
		{"info is no cause", []spec{{"a", "A", SeverityInfo, 0}, {"p1", "P1", SeverityError, 0}},
			map[string]string{"a": "", "p1": ""}},
		{"info can be a symptom", []spec{{"a", "A", SeverityError, 0}, {"p1", "P1", SeverityInfo, 0}},
			map[string]string{"a": "", "p1": "a"}},
		{"uses spreads backwards", []spec{{"p2", "P2", SeverityError, 0}, {"p3", "P3", SeverityError, 0}},
			map[string]string{"p2": "", "p3": "p2"}},
		{"severer root wins", []spec{{"p1", "P1", SeverityWarning, 9}, {"a2", "A2", SeverityError, 0}, {"p2", "P2", SeverityError, 0}},
			map[string]string{"p1": "", "a2": "", "p2": "a2"}},
		{"older root wins", []spec{{"p1", "P1", SeverityError, 0}, {"a2", "A2", SeverityError, 5}, {"p2", "P2", SeverityError, 9}},
			map[string]string{"p1": "", "a2": "", "p2": "a2"}},
		{"cycle: oldest is root", []spec{{"p4", "P4", SeverityError, 1}, {"p5", "P5", SeverityError, 2}},
			map[string]string{"p4": "p5", "p5": ""}},
		{"same element", []spec{{"x", "P1", SeverityError, 0}, {"y", "P1", SeverityError, 1}},
			map[string]string{"x": "", "y": ""}},
		{"member and represents carry nothing", []spec{{"g", "G", SeverityError, 0}, {"p1", "P1", SeverityError, 0}, {"p4", "P4", SeverityError, 0}},
			map[string]string{"g": "", "p1": "", "p4": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			active := map[uuid.UUID]Finding{}
			names := map[uuid.UUID]string{uuid.Nil: ""}
			byName := map[string]Finding{}
			for _, s := range tt.findings {
				f := Finding{ID: uuid.New(), ElementID: ids[s.element], Severity: s.severity, RaisedAt: t0.Add(-time.Duration(s.age) * time.Minute)}
				active[f.ID], names[f.ID], byName[s.name] = f, s.name, f
			}
			calc := newCauseCalc(g, active)
			for name, want := range tt.want {
				if got := names[calc.cause(byName[name])]; got != want {
					t.Errorf("cause(%s) = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestCauseAssignment(t *testing.T) {
	b := newBench(t, tempRule(func(r *Rule) { r.Severity = SeverityError }))
	b.state.notLive = map[uuid.UUID]bool{b.source: true}
	b.state.unreachable = map[uuid.UUID]string{b.source: "disconnected"}
	if got := summary(b.tick(time.Second)); got != "+" {
		t.Fatalf("unreachable: %q", got)
	}
	unreachable := b.store.raised[0]

	// Der Port gehört zur Quelle: Sein Befund entsteht als Symptom des Zugangs.
	b.state.notLive = nil
	b.state.unreachable = nil
	b.state.facts[b.port]["temp"] = 85.0
	changes, err := b.ev.Evaluate(t.Context(), b.now, []uuid.UUID{b.port}, b.state)
	if err != nil || summary(changes) != "+" || b.store.raised[1].CausedByID != unreachable.ID {
		t.Fatalf("Evaluate = %q, %v; caused by %s, want %s", summary(changes), err, b.store.raised[1].CausedByID, unreachable.ID)
	}
	port := b.store.raised[1].ID

	// Die Quelle ist wieder Live: Der Zugang endet, der Port wird selbst Ursache.
	b.store.causeErr = errors.New("db down")
	changes, err = b.ev.Tick(t.Context(), b.now, b.state)
	if summary(changes) != "-resolved" || !errors.Is(err, b.store.causeErr) {
		t.Fatalf("Tick = %q, %v; want resolved and failed SetFindingCause", summary(changes), err)
	}
	if b.ev.Findings()[port].CausedByID != unreachable.ID {
		t.Error("memory changed although SetFindingCause failed")
	}
	b.store.causeErr = nil
	changes = b.tick(time.Second)
	if summary(changes) != "~" || changes[0].CauseSet != port || changes[0].CausedBy != uuid.Nil {
		t.Fatalf("retry: %+v", changes)
	}
	if b.store.causes[port] != uuid.Nil || b.ev.Findings()[port].CausedByID != uuid.Nil {
		t.Error("cause not cleared")
	}
	if got := summary(b.tick(time.Second)); got != "" {
		t.Errorf("nothing changed: %q", got)
	}
}
