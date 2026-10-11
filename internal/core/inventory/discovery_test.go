package inventory

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// fakeStore ist die Attrappe des Stores. ApplyDiscovery bildet FinishDiscovery nach: Elemente der
// Quelle, die nicht gesehen wurden, verschwinden.
type fakeStore struct {
	Store // nicht nachgebildete Methoden: panic

	inv         *Inventory // Stand vor dem Aufruf, gelesen wie die Datenbank
	discoveries []Discovery
	err         error
}

func (f *fakeStore) ApplyDiscovery(_ context.Context, d Discovery) ([]uuid.UUID, error) {
	f.discoveries = append(f.discoveries, d)
	if f.err != nil {
		return nil, f.err
	}
	var missing []uuid.UUID
	for id, e := range f.inv.elements {
		if e.SourceID == d.SourceID && e.Present && e.Kind != KindAccess && !slices.Contains(d.Seen, id) {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

func testDrivers(name string) (sdk.Driver, bool) {
	if name != "videoxlink" && name != "nmos" {
		return sdk.Driver{}, false
	}
	return sdk.Driver{Name: name, Actions: []sdk.ActionDef{{Name: vocab.ActionReboot}}}, true
}

func (tr tree) snapshot() sdk.Snapshot {
	return sdk.Snapshot{
		GlobalRef: "xlink:abc",
		Elements: []sdk.Element{
			{ExternalID: "sdi/1", ParentID: "card/1", Kind: vocab.ElementPort, Transport: "sdi", Direction: vocab.DirectionIn, Name: "SDI 1"},
			{ExternalID: "card/1", Kind: vocab.ElementModule, Name: "Card 1"},
		},
		Links: []sdk.Link{{Type: sdk.LinkFeeds, From: sdk.Endpoint{ExternalID: "card/1"}, To: sdk.Endpoint{ExternalID: "sdi/1"}}},
	}
}

// clock setzt die Uhr des Inventars auf t und liefert eine Funktion zum Vorstellen.
func (tr tree) clock(t time.Time) func(time.Duration) {
	tr.inv.now = func() time.Time { return t }
	return func(d time.Duration) { t = t.Add(d) }
}

func (tr tree) store() *fakeStore {
	f := tr.inv.store.(*fakeStore)
	f.inv = tr.inv
	return f
}

func TestApplySnapshotUnchanged(t *testing.T) {
	tr := newTree(t)
	store := tr.store()
	advance := tr.clock(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC))

	// Der erste Snapshot nach dem Start schreibt last_seen_at, die folgenden erst nach 5 min.
	for i, want := range []int{1, 1, 2} {
		diff, err := tr.inv.ApplySnapshot(t.Context(), tr.source, tr.snapshot())
		if err != nil {
			t.Fatalf("ApplySnapshot #%d: %v", i, err)
		}
		if !emptyDiff(diff) {
			t.Errorf("ApplySnapshot #%d: diff = %+v, want empty", i, diff)
		}
		if got := len(store.discoveries); got != want {
			t.Fatalf("ApplySnapshot #%d: %d store calls, want %d", i, got, want)
		}
		advance(3 * time.Minute)
	}
	if d := store.discoveries[1]; len(d.Upsert) != 0 || len(d.AddLinks) != 0 || len(d.RemoveLinks) != 0 || len(d.Seen) != 3 {
		t.Errorf("discovery = %+v, want only seen", d)
	}
}

func TestApplySnapshotNewSource(t *testing.T) {
	tr := newTree(t)
	store := tr.store()
	source, access := id(), id()
	tr.inv.Load(Snapshot{
		Sources:  []Source{{ID: source, DeviceID: tr.device, Driver: "videoxlink", AccessElementID: access}},
		Elements: []Element{{ID: access, Kind: KindAccess, ParentID: tr.device, DeviceID: tr.device, SourceID: source, Present: true}},
	})
	s := tr.snapshot()
	s.GlobalRef = "xlink:def"
	s.Identity = sdk.Identity{Vendor: "Videon", Serial: "X8A9001"}
	s.Elements[0].Settings = map[string]sdk.Setting{"state.running": {Type: sdk.SettingBool}}

	diff, err := tr.inv.ApplySnapshot(t.Context(), source, s)
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	card, _ := tr.inv.ByExternalID(source, "card/1")
	port, _ := tr.inv.ByExternalID(source, "sdi/1")
	if !slices.Equal(diff.Added, []uuid.UUID{card, port}) || !diff.LinksChanged {
		t.Errorf("diff = %+v, want card and port added, links changed", diff)
	}
	d := store.discoveries[0]
	if len(d.Upsert) != 2 || d.Upsert[0].ID != card || d.Upsert[0].ParentID != access || d.Upsert[1].ParentID != card {
		t.Errorf("upsert = %+v, want card under access before port under card", d.Upsert)
	}
	if d.Identity == nil || d.Identity.Serial != "X8A9001" || d.GlobalRef == nil || *d.GlobalRef != "xlink:def" {
		t.Errorf("identity = %v, global ref = %v", d.Identity, d.GlobalRef)
	}
	if e, _ := tr.inv.Element(port); !e.Present || !e.Monitored || e.FirstSeenAt.IsZero() || !jsonEqual(e.Settings, json.RawMessage(`{"state.running":{"type":"bool"}}`)) {
		t.Errorf("port = %+v", e)
	}
	if src, _ := tr.inv.Source(source); src.Identity.Vendor != "Videon" {
		t.Errorf("source identity = %+v", src.Identity)
	}
	if got, _ := tr.inv.Element(access); got.GlobalRef != "xlink:def" || tr.inv.byRef["xlink:def"] != access {
		t.Errorf("access global ref = %q", got.GlobalRef)
	}
}

func TestApplySnapshotPresence(t *testing.T) {
	tr := newTree(t)
	tr.store()
	s := tr.snapshot()
	s.Elements = s.Elements[1:] // Port fehlt

	diff, err := tr.inv.ApplySnapshot(t.Context(), tr.source, s)
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	if !slices.Equal(diff.Missing, []uuid.UUID{tr.port}) || !diff.LinksChanged {
		t.Errorf("diff = %+v, want port missing, link removed", diff)
	}
	if e, _ := tr.inv.Element(tr.port); e.Present {
		t.Error("port still present")
	}

	diff, err = tr.inv.ApplySnapshot(t.Context(), tr.source, tr.snapshot())
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	if !slices.Equal(diff.Returned, []uuid.UUID{tr.port}) || len(diff.Added) != 0 {
		t.Errorf("diff = %+v, want port returned", diff)
	}
	if e, _ := tr.inv.Element(tr.port); !e.Present {
		t.Error("port not present")
	}
}

func TestApplySnapshotChanged(t *testing.T) {
	tr := newTree(t)
	store := tr.store()
	// So kommen JSON-Spalten aus PostgreSQL zurück: andere Reihenfolge, andere Leerzeichen.
	e, _ := tr.inv.Element(tr.port)
	e.Attributes = json.RawMessage(`{"b": 2, "a": 1}`)
	tr.inv.putElement(e)
	s := tr.snapshot()
	s.Elements[0].Attributes = json.RawMessage(`{"a":1,"b":2}`)
	s.Elements[1].Name = "Card One"

	diff, err := tr.inv.ApplySnapshot(t.Context(), tr.source, s)
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	if !slices.Equal(diff.Changed, []uuid.UUID{tr.card}) || diff.LinksChanged {
		t.Errorf("diff = %+v, want only card changed", diff)
	}
	if up := store.discoveries[0].Upsert; len(up) != 1 || up[0].Label != "Decoder" {
		t.Errorf("upsert = %+v, want card with user label kept", up)
	}
	if got := tr.inv.Path(tr.port); got != "X8 / Decoder / SDI 1" {
		t.Errorf("Path = %q", got)
	}
}

func TestApplySnapshotDropsInvalid(t *testing.T) {
	tr := newTree(t)
	store := tr.store()
	s := tr.snapshot()
	s.Elements = append(s.Elements,
		sdk.Element{ExternalID: "x/1", Kind: vocab.ElementAccess},                          // reserviert
		sdk.Element{ExternalID: "x/2", ParentID: "x/1", Kind: vocab.ElementPort},           // Eltern verworfen
		sdk.Element{ExternalID: "x/3", ParentID: "nope", Kind: vocab.ElementPort},          // Eltern unbekannt
		sdk.Element{ExternalID: "x/4", ParentID: "x/5", Kind: vocab.ElementPort},           // Zyklus
		sdk.Element{ExternalID: "x/5", ParentID: "x/4", Kind: vocab.ElementPort},           // Zyklus
		sdk.Element{ExternalID: "card/1", Kind: vocab.ElementSensor},                       // doppelt
		sdk.Element{ExternalID: "x/6", Kind: vocab.ElementPort, GlobalRef: "nmos:1"},       // fremder Namensraum
		sdk.Element{ExternalID: "x/7", Kind: vocab.ElementPort, Actions: []string{"stop"}}, // nicht im Katalog
	)
	s.Links = append(s.Links,
		sdk.Link{Type: sdk.LinkFeeds, From: sdk.Endpoint{ExternalID: "x/2"}, To: sdk.Endpoint{ExternalID: "sdi/1"}},
		sdk.Link{Type: "member", From: sdk.Endpoint{ExternalID: "card/1"}, To: sdk.Endpoint{ExternalID: "sdi/1"}},
		sdk.Link{Type: sdk.LinkRepresents, From: sdk.Endpoint{ExternalID: "card/1"}, To: sdk.Endpoint{ExternalID: "sdi/1"}},
	)

	diff, err := tr.inv.ApplySnapshot(t.Context(), tr.source, s)
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	if !emptyDiff(diff) {
		t.Errorf("diff = %+v, want empty", diff)
	}
	if d := store.discoveries[0]; len(d.Upsert) != 0 || len(d.AddLinks) != 0 || len(d.Seen) != 3 {
		t.Errorf("discovery = %+v, want nothing new", d)
	}
}

func TestApplySnapshotLinksAcrossSources(t *testing.T) {
	tr := newTree(t)
	store := tr.store()
	s := tr.snapshot()
	s.Links = append(s.Links, sdk.Link{Type: sdk.LinkUses, From: sdk.Endpoint{ExternalID: "sdi/1"}, To: sdk.Endpoint{GlobalRef: "nmos:recv"}})

	// Das Ziel ist noch unbekannt: Der Link hält die global_ref.
	if _, err := tr.inv.ApplySnapshot(t.Context(), tr.source, s); err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	add := store.discoveries[0].AddLinks
	if len(add) != 1 || add[0].ToRef != "nmos:recv" || add[0].ToID != uuid.Nil {
		t.Fatalf("add links = %+v, want one unresolved", add)
	}
	link := add[0].ID

	// Eine andere Quelle meldet das Ziel: Der Link wird aufgelöst.
	source, access := id(), id()
	tr.inv.Load(Snapshot{
		Sources:  []Source{{ID: source, DeviceID: id(), Driver: "nmos", AccessElementID: access}},
		Elements: []Element{{ID: access, Kind: KindAccess, SourceID: source, Present: true}},
	})
	diff, err := tr.inv.ApplySnapshot(t.Context(), source, sdk.Snapshot{Elements: []sdk.Element{
		{ExternalID: "recv", Kind: vocab.ElementReceiver, GlobalRef: "nmos:recv"},
	}})
	if err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	recv, _ := tr.inv.ByExternalID(source, "recv")
	if l := tr.inv.Links()[link]; l.ToID != recv || l.ToRef != "" || !diff.LinksChanged {
		t.Errorf("link = %+v, diff = %+v; want resolved to %s", l, diff, recv)
	}

	// Derselbe Snapshot der ersten Quelle ändert am aufgelösten Link nichts.
	diff, err = tr.inv.ApplySnapshot(t.Context(), tr.source, s)
	if err != nil || diff.LinksChanged {
		t.Errorf("diff = %+v, %v; want links unchanged", diff, err)
	}
}

func TestApplySnapshotErrors(t *testing.T) {
	tests := []struct {
		name     string
		storeErr error
		change   func(tr tree, s *sdk.Snapshot)
		want     error
	}{
		{"global ref of another source", nil, func(tr tree, s *sdk.Snapshot) {
			other, access := id(), id()
			tr.inv.Load(Snapshot{
				Sources:  []Source{{ID: other, Driver: "videoxlink", AccessElementID: access}},
				Elements: []Element{{ID: access, Kind: KindAccess, SourceID: other, GlobalRef: "xlink:taken"}},
			})
			s.Elements[0].GlobalRef = "xlink:taken"
		}, ErrGlobalRefConflict},
		{"global ref twice", nil, func(_ tree, s *sdk.Snapshot) {
			s.Elements[0].GlobalRef = "xlink:abc"
		}, ErrGlobalRefConflict},
		{"store fails", errors.New("db down"), func(_ tree, s *sdk.Snapshot) {
			s.Elements[1].Name = "Card One"
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTree(t)
			store := tr.store()
			store.err = tt.storeErr
			s := tr.snapshot()
			tt.change(tr, &s)
			before := tr.inv.Elements()

			_, err := tr.inv.ApplySnapshot(t.Context(), tr.source, s)
			want := cmp.Or(tt.want, tt.storeErr)
			if !errors.Is(err, want) {
				t.Fatalf("ApplySnapshot = %v, want %v", err, want)
			}
			if tt.storeErr == nil && len(store.discoveries) != 0 {
				t.Error("store called despite conflict")
			}
			after := tr.inv.Elements()
			for id, e := range before {
				if !sameDriverFields(e, after[id]) || e.Present != after[id].Present {
					t.Errorf("element %s changed in memory", e.ExternalID)
				}
			}
		})
	}
}

func emptyDiff(d Diff) bool {
	return len(d.Added)+len(d.Changed)+len(d.Missing)+len(d.Returned)+len(d.Deleted) == 0 && !d.LinksChanged
}
