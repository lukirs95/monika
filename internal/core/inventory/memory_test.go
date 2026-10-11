package inventory

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

// tree ist ein kleines Inventar: Gerät X8 mit einer Quelle, ihrem Zugang, einer Karte und
// einem Port darunter. snapshot meldet genau diesen Stand.
type tree struct {
	inv                                *Inventory
	device, source, access, card, port uuid.UUID
}

func newTree(t *testing.T) tree {
	t.Helper()
	tr := tree{device: id(), source: id(), access: id(), card: id(), port: id()}
	tr.inv = New(&fakeStore{}, testDrivers, slog.New(slog.DiscardHandler))
	tr.inv.Load(Snapshot{
		Sources: []Source{{ID: tr.source, DeviceID: tr.device, Driver: "videoxlink", AccessElementID: tr.access}},
		Elements: []Element{
			{ID: tr.device, Kind: KindDevice, DeviceID: tr.device, Label: "X8", Present: true},
			{ID: tr.access, Kind: KindAccess, ParentID: tr.device, DeviceID: tr.device, SourceID: tr.source, GlobalRef: "xlink:abc", Present: true},
			{ID: tr.card, Kind: "module", ParentID: tr.access, DeviceID: tr.device, SourceID: tr.source, ExternalID: "card/1", Name: "Card 1", Label: "Decoder", Present: true},
			{ID: tr.port, Kind: "port", ParentID: tr.card, DeviceID: tr.device, SourceID: tr.source, ExternalID: "sdi/1", Transport: "sdi", Direction: DirectionIn, Name: "SDI 1", Present: true},
		},
		Links: []Link{{ID: id(), Type: LinkFeeds, SourceID: tr.source, FromID: tr.card, ToID: tr.port}},
	})
	return tr
}

func id() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func TestPath(t *testing.T) {
	tr := newTree(t)
	tests := []struct {
		name string
		id   uuid.UUID
		want string
	}{
		{"device", tr.device, "X8"},
		{"access is skipped", tr.access, "X8"},
		{"label wins over name", tr.card, "X8 / Decoder"},
		{"name without label", tr.port, "X8 / Decoder / SDI 1"},
		{"unknown", id(), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tr.inv.Path(tt.id); got != tt.want {
				t.Errorf("Path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestByExternalID(t *testing.T) {
	tr := newTree(t)
	tests := []struct {
		name   string
		source uuid.UUID
		ext    string
		want   uuid.UUID
		ok     bool
	}{
		{"element", tr.source, "sdi/1", tr.port, true},
		{"access", tr.source, "", tr.access, true},
		{"unknown external id", tr.source, "sdi/9", uuid.Nil, false},
		{"other source", id(), "sdi/1", uuid.Nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tr.inv.ByExternalID(tt.source, tt.ext)
			if got != tt.want || ok != tt.ok {
				t.Errorf("ByExternalID = %v, %v; want %v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestFactsByID(t *testing.T) {
	tr := newTree(t)
	got, unknown := tr.inv.FactsByID(tr.source, map[string]sdk.Facts{
		"sdi/1":  {"state.signal_present": true},
		"":       {"system.temperature_c": 41.5},
		"sdi/9":  {"state.signal_present": false},
		"card/7": {"state.running": true},
	})
	if len(got) != 2 || got[tr.port]["state.signal_present"] != true || got[tr.access]["system.temperature_c"] != 41.5 {
		t.Errorf("facts = %v", got)
	}
	if want := []string{"card/7", "sdi/9"}; !slices.Equal(unknown, want) {
		t.Errorf("unknown = %v, want %v", unknown, want)
	}
}

func TestDeleteElement(t *testing.T) {
	tr := newTree(t)
	tr.inv.deleteElement(tr.port)

	if _, ok := tr.inv.Element(tr.port); ok {
		t.Error("element still there")
	}
	if _, ok := tr.inv.ByExternalID(tr.source, "sdi/1"); ok {
		t.Error("external id still resolves")
	}
	if n := len(tr.inv.Links()); n != 0 {
		t.Errorf("links = %d, want 0", n)
	}
}

func TestGlobalRef(t *testing.T) {
	tr := newTree(t)
	if got := tr.inv.byRef["xlink:abc"]; got != tr.access {
		t.Fatalf("byRef = %v, want access", got)
	}

	e, _ := tr.inv.Element(tr.access)
	e.GlobalRef = "xlink:def"
	tr.inv.putElement(e)
	if _, ok := tr.inv.byRef["xlink:abc"]; ok {
		t.Error("old global_ref still resolves")
	}
	if got := tr.inv.byRef["xlink:def"]; got != tr.access {
		t.Errorf("byRef = %v, want access", got)
	}
}
