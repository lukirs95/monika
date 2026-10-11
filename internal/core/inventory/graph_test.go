package inventory

import (
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// graphFixture erweitert den Baum: zweiter Port unter der Karte, ein zweites Gerät mit
// deaktivierter Quelle und Gruppen, auch verschachtelt.
//
//	X8 ─ access ─ card ─┬─ port        g1 ∋ port, x9      g3 ∋ card
//	                    └─ port2       g2 ∋ g1            g4 ∋ X8, port
//	X9 ─ access2 ─ q
type graphFixture struct {
	tree
	port2, x9, access2, q, g1, g2, g3, g4 uuid.UUID
}

func newGraphFixture(t *testing.T) graphFixture {
	t.Helper()
	f := graphFixture{tree: newTree(t)}
	f.port2, f.x9, f.access2, f.q = id(), id(), id(), id()
	f.g1, f.g2, f.g3, f.g4 = id(), id(), id(), id()
	source2 := id()
	member := func(group, element uuid.UUID) Link {
		return Link{ID: id(), Type: LinkMember, FromID: group, ToID: element}
	}
	f.inv.Load(Snapshot{
		Sources: []Source{{ID: source2, DeviceID: f.x9, Driver: "videoxlink", AccessElementID: f.access2}},
		Elements: []Element{
			{ID: f.port2, Kind: "port", ParentID: f.card, DeviceID: f.device, SourceID: f.source, ExternalID: "sdi/2", Monitored: true},
			{ID: f.x9, Kind: KindDevice, DeviceID: f.x9, Monitored: true},
			{ID: f.access2, Kind: KindAccess, ParentID: f.x9, DeviceID: f.x9, SourceID: source2, Monitored: true},
			{ID: f.q, Kind: "port", ParentID: f.access2, DeviceID: f.x9, SourceID: source2, ExternalID: "q", Monitored: true},
			{ID: f.g1, Kind: KindGroup, Monitored: true},
			{ID: f.g2, Kind: KindGroup, Monitored: true},
			{ID: f.g3, Kind: KindGroup, Monitored: true},
			{ID: f.g4, Kind: KindGroup, Monitored: true},
		},
		Links: []Link{
			member(f.g1, f.port), member(f.g1, f.x9), member(f.g2, f.g1), member(f.g3, f.card),
			member(f.g4, f.device), member(f.g4, f.port),
		},
	})
	// Der Baum aus newTree ist überwacht und seine Quelle aktiviert.
	src, _ := f.inv.Source(f.source)
	src.Enabled = true
	f.inv.sources[f.source] = src
	for _, id := range []uuid.UUID{f.device, f.access, f.card, f.port} {
		e, _ := f.inv.Element(id)
		e.Monitored = true
		f.inv.putElement(e)
	}
	return f
}

func TestGraphAncestorsAndScopes(t *testing.T) {
	f := newGraphFixture(t)
	g := f.inv.Graph()

	if got := slices.Collect(g.Ancestors(f.port)); !slices.Equal(got, []uuid.UUID{f.card, f.access, f.device}) {
		t.Errorf("Ancestors = %v", got)
	}
	want := map[uuid.UUID]int{
		f.port: 0, f.card: 1, f.access: 2, f.device: 3,
		f.g1: 1, f.g4: 1, // direkt Mitglied
		f.g2: 2, // über g1
		f.g3: 2, // über die Karte
	}
	if got := g.Scopes(f.port); !maps.Equal(got, want) {
		t.Errorf("Scopes = %v, want %v", got, want)
	}
	if got := slices.Collect(g.Children(f.card)); !slices.Equal(got, []uuid.UUID{f.port, f.port2}) {
		t.Errorf("Children = %v", got)
	}
}

func TestGraphInScope(t *testing.T) {
	f := newGraphFixture(t)
	g := f.inv.Graph()
	tests := []struct {
		name      string
		scope, id uuid.UUID
		want      bool
	}{
		{"global", uuid.Nil, f.port, true},
		{"itself", f.port, f.port, true},
		{"parent", f.card, f.port, true},
		{"device", f.device, f.port, true},
		{"child is not scope of parent", f.port, f.card, false},
		{"other device", f.x9, f.port, false},
		{"group member", f.g1, f.port, true},
		{"descendant of group member", f.g1, f.q, true},
		{"nested group", f.g2, f.q, true},
		{"group via ancestor", f.g3, f.port2, true},
		{"sibling of group member", f.g1, f.port2, false},
		{"group itself", f.g1, f.g1, true},
		{"unknown scope", id(), f.port, false},
		{"unknown element", f.device, id(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := g.InScope(tt.scope, tt.id); got != tt.want {
				t.Errorf("InScope = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGraphScopeElements(t *testing.T) {
	f := newGraphFixture(t)
	g := f.inv.Graph()
	tests := []struct {
		name  string
		scope uuid.UUID
		want  []uuid.UUID
	}{
		{"device", f.device, []uuid.UUID{f.device, f.access, f.card, f.port, f.port2}},
		{"group", f.g1, []uuid.UUID{f.g1, f.port, f.x9, f.access2, f.q}},
		{"nested group", f.g2, []uuid.UUID{f.g2, f.g1, f.port, f.x9, f.access2, f.q}},
		{"device and its port count once", f.g4, []uuid.UUID{f.g4, f.device, f.port, f.access, f.card, f.port2}},
		{"unknown", id(), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slices.Collect(g.ScopeElements(tt.scope))
			if len(got) != len(tt.want) || !sameSet(got, tt.want) {
				t.Errorf("ScopeElements = %v, want %v", got, tt.want)
			}
			for _, id := range got {
				if !g.InScope(tt.scope, id) {
					t.Errorf("%s in ScopeElements but not InScope", id)
				}
			}
		})
	}
	if n := len(slices.Collect(g.ScopeElements(uuid.Nil))); n != len(f.inv.elements) {
		t.Errorf("global scope = %d elements, want %d", n, len(f.inv.elements))
	}
}

func sameSet(a, b []uuid.UUID) bool {
	set := map[uuid.UUID]bool{}
	for _, id := range a {
		set[id] = true
	}
	return len(set) == len(a) && !slices.ContainsFunc(b, func(id uuid.UUID) bool { return !set[id] })
}

func TestGraphLinks(t *testing.T) {
	f := newGraphFixture(t)
	g := f.inv.Graph()
	types := func(links []Link) []LinkType {
		out := make([]LinkType, 0, len(links))
		for _, l := range links {
			out = append(out, l.Type)
		}
		slices.Sort(out)
		return out
	}
	if got := types(slices.Collect(g.Outgoing(f.card))); !slices.Equal(got, []LinkType{LinkFeeds}) {
		t.Errorf("Outgoing(card) = %v", got)
	}
	if got := types(slices.Collect(g.Incoming(f.port))); !slices.Equal(got, []LinkType{LinkFeeds, LinkMember, LinkMember}) {
		t.Errorf("Incoming(port) = %v", got)
	}
}

func TestGraphMonitored(t *testing.T) {
	f := newGraphFixture(t)
	if g := f.inv.Graph(); !g.Monitored(f.port) || g.Monitored(f.q) {
		t.Errorf("Monitored(port) = %v, Monitored(q) = %v; want true, false (source disabled)", g.Monitored(f.port), g.Monitored(f.q))
	}

	before := f.inv.Graph()
	if _, err := f.inv.UpdateUserFields(t.Context(), id(), f.card, UserFields{Monitored: false}); err != nil {
		t.Fatalf("UpdateUserFields: %v", err)
	}
	after := f.inv.Graph()
	if after == before {
		t.Fatal("graph not rebuilt after a change")
	}
	if after.Monitored(f.port) || !before.Monitored(f.port) {
		t.Error("port: want unmonitored through its parent now, old graph unchanged")
	}
	if !after.Monitored(f.device) {
		t.Error("device: parent stays monitored")
	}
	if f.inv.Graph() != after {
		t.Error("graph rebuilt without a change")
	}
}
