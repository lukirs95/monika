package inventory

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestCreate(t *testing.T) {
	tr := newTree(t)
	tr.store()
	ctx, actor := t.Context(), id()

	device := id()
	diff, err := tr.inv.CreateDevice(ctx, actor, Element{ID: device, Label: "X9", Kind: KindGroup})
	if err != nil || !slices.Equal(diff.Added, []uuid.UUID{device}) {
		t.Fatalf("CreateDevice = %+v, %v", diff, err)
	}
	if e, _ := tr.inv.Element(device); e.Kind != KindDevice || e.DeviceID != device || !e.Present || !e.Monitored {
		t.Errorf("device = %+v", e)
	}

	source := id()
	diff, err = tr.inv.CreateSource(ctx, actor, Source{ID: source, DeviceID: device, Driver: "videoxlink", Address: "10.0.0.9", Enabled: true})
	if err != nil || len(diff.Added) != 1 {
		t.Fatalf("CreateSource = %+v, %v", diff, err)
	}
	src, _ := tr.inv.Source(source)
	access, _ := tr.inv.ByExternalID(source, "")
	if src.AccessElementID != access || access != diff.Added[0] || src.Address != "10.0.0.9" {
		t.Errorf("source = %+v, access %s", src, access)
	}
	if e, _ := tr.inv.Element(access); e.Kind != KindAccess || e.ParentID != device || e.Name != "videoxlink" {
		t.Errorf("access = %+v", e)
	}

	// Mit Schema: Anlegen und Ändern prüfen die Konfiguration.
	nmos := id()
	if _, err := tr.inv.CreateSource(ctx, actor, Source{ID: nmos, DeviceID: device, Driver: "nmos", Config: []byte(`{"port":3000}`)}); err != nil {
		t.Fatalf("CreateSource with schema: %v", err)
	}
	if _, err := tr.inv.UpdateSource(ctx, actor, Source{ID: nmos, Config: []byte(`{"port":3000,"user":"admin"}`)}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("UpdateSource with an unknown field = %v, want ErrInvalidConfig", err)
	}
	if src, _ := tr.inv.Source(nmos); string(src.Config) != `{"port":3000}` {
		t.Errorf("config after a rejected update = %s", src.Config)
	}

	group := id()
	if _, err := tr.inv.CreateGroup(ctx, actor, Element{ID: group, Label: "Studio A"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if e, _ := tr.inv.Element(group); e.Kind != KindGroup || e.Label != "Studio A" {
		t.Errorf("group = %+v", e)
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name    string
		del     func(tr tree, group uuid.UUID) (Diff, error)
		deleted func(tr tree, group, ipmi uuid.UUID) []uuid.UUID
		source  bool // die Quelle videoxlink bleibt
	}{
		{"device", func(tr tree, _ uuid.UUID) (Diff, error) {
			return tr.inv.DeleteDevice(t.Context(), id(), tr.device)
		}, func(tr tree, _, ipmi uuid.UUID) []uuid.UUID {
			return []uuid.UUID{tr.device, tr.access, tr.card, tr.port, ipmi}
		}, false},
		{"source", func(tr tree, _ uuid.UUID) (Diff, error) {
			return tr.inv.DeleteSource(t.Context(), id(), tr.source)
		}, func(tr tree, _, _ uuid.UUID) []uuid.UUID {
			return []uuid.UUID{tr.access, tr.card, tr.port}
		}, false},
		{"group", func(tr tree, group uuid.UUID) (Diff, error) {
			return tr.inv.DeleteGroup(t.Context(), id(), group)
		}, func(_ tree, group, _ uuid.UUID) []uuid.UUID {
			return []uuid.UUID{group}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTree(t)
			tr.store()
			// Eine zweite Quelle am Gerät und eine Gruppe mit dem Port als Mitglied.
			ipmiSource, ipmi, group, member := id(), id(), id(), id()
			tr.inv.Load(Snapshot{
				Sources: []Source{{ID: ipmiSource, DeviceID: tr.device, Driver: "nmos", AccessElementID: ipmi}},
				Elements: []Element{
					{ID: ipmi, Kind: KindAccess, ParentID: tr.device, DeviceID: tr.device, SourceID: ipmiSource},
					{ID: group, Kind: KindGroup},
				},
				Links: []Link{{ID: member, Type: LinkMember, FromID: group, ToID: tr.port}},
			})

			diff, err := tt.del(tr, group)
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			want := tt.deleted(tr, group, ipmi)
			slices.SortFunc(want, uuidCompare)
			slices.SortFunc(diff.Deleted, uuidCompare)
			if !slices.Equal(diff.Deleted, want) || !diff.LinksChanged {
				t.Errorf("diff = %+v, want deleted %v and links changed", diff, want)
			}
			for _, id := range want {
				if _, ok := tr.inv.Element(id); ok {
					t.Errorf("element %s still there", id)
				}
			}
			if _, ok := tr.inv.Links()[member]; ok {
				t.Error("member link still there")
			}
			if _, ok := tr.inv.Source(tr.source); ok != tt.source {
				t.Errorf("source kept = %v, want %v", ok, tt.source)
			}
			if _, ok := tr.inv.ByExternalID(tr.source, "sdi/1"); ok != tt.source {
				t.Errorf("external id resolves = %v, want %v", ok, tt.source)
			}
		})
	}
}

func uuidCompare(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) }

func TestUserFieldsAndMembers(t *testing.T) {
	tr := newTree(t)
	tr.store()
	ctx, actor := t.Context(), id()

	if _, err := tr.inv.UpdateUserFields(ctx, actor, tr.port, UserFields{Label: "Cam 1", Monitored: true}); err != nil {
		t.Fatalf("UpdateUserFields: %v", err)
	}
	if got := tr.inv.Path(tr.port); got != "X8 / Decoder / Cam 1" {
		t.Errorf("Path = %q", got)
	}

	group, member := id(), id()
	if _, err := tr.inv.CreateGroup(ctx, actor, Element{ID: group}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	diff, err := tr.inv.AddGroupMember(ctx, actor, Link{ID: member, Type: LinkFeeds, FromID: group, ToID: tr.port, Label: "Cam"})
	if err != nil || !diff.LinksChanged {
		t.Fatalf("AddGroupMember = %+v, %v", diff, err)
	}
	if l := tr.inv.Links()[member]; l.Type != LinkMember || l.FromID != group || l.ToID != tr.port || l.Label != "Cam" {
		t.Errorf("link = %+v", l)
	}
	if _, err := tr.inv.UpdateGroupMember(ctx, actor, Link{ID: member, Label: "Kamera", FromID: id()}); err != nil {
		t.Fatalf("UpdateGroupMember: %v", err)
	}
	if l := tr.inv.Links()[member]; l.Label != "Kamera" || l.FromID != group {
		t.Errorf("link = %+v, want new label, same group", l)
	}
	if _, err := tr.inv.RemoveGroupMember(ctx, actor, member); err != nil {
		t.Fatalf("RemoveGroupMember: %v", err)
	}
	if _, ok := tr.inv.Links()[member]; ok {
		t.Error("member link still there")
	}
}

// TestCommandErrors prüft die Vorbedingungen im Inventar (ohne Aufruf des Stores) und dass ein
// Fehler des Stores den Speicher unverändert lässt.
func TestCommandErrors(t *testing.T) {
	storeErr := errors.New("db down")
	feeds := func(tr tree) uuid.UUID {
		for id, l := range tr.inv.Links() {
			if l.Type == LinkFeeds {
				return id
			}
		}
		return uuid.Nil
	}
	tests := []struct {
		name     string
		storeErr error
		run      func(tr tree) (Diff, error)
		want     error
	}{
		{"device without ID", nil, func(tr tree) (Diff, error) {
			return tr.inv.CreateDevice(t.Context(), id(), Element{})
		}, ErrMissingID},
		{"delete unknown device", nil, func(tr tree) (Diff, error) {
			return tr.inv.DeleteDevice(t.Context(), id(), id())
		}, ErrNotFound},
		{"delete card as device", nil, func(tr tree) (Diff, error) {
			return tr.inv.DeleteDevice(t.Context(), id(), tr.card)
		}, ErrNotFound},
		{"source on unknown device", nil, func(tr tree) (Diff, error) {
			return tr.inv.CreateSource(t.Context(), id(), Source{ID: id(), DeviceID: id(), Driver: "videoxlink"})
		}, ErrNotFound},
		{"config against the schema", nil, func(tr tree) (Diff, error) {
			return tr.inv.CreateSource(t.Context(), id(), Source{ID: id(), DeviceID: tr.device, Driver: "nmos", Config: []byte(`{"port":"x"}`)})
		}, ErrInvalidConfig},
		{"config missing a required field", nil, func(tr tree) (Diff, error) {
			return tr.inv.CreateSource(t.Context(), id(), Source{ID: id(), DeviceID: tr.device, Driver: "nmos"})
		}, ErrInvalidConfig},
		{"source with unknown driver", nil, func(tr tree) (Diff, error) {
			return tr.inv.CreateSource(t.Context(), id(), Source{ID: id(), DeviceID: tr.device, Driver: "acme"})
		}, ErrUnknownDriver},
		{"update unknown source", nil, func(tr tree) (Diff, error) {
			return tr.inv.UpdateSource(t.Context(), id(), Source{ID: id()})
		}, ErrNotFound},
		{"delete device as group", nil, func(tr tree) (Diff, error) {
			return tr.inv.DeleteGroup(t.Context(), id(), tr.device)
		}, ErrNotFound},
		{"member of a non-group", nil, func(tr tree) (Diff, error) {
			return tr.inv.AddGroupMember(t.Context(), id(), Link{ID: id(), FromID: tr.device, ToID: tr.port})
		}, ErrNotAGroup},
		{"remove a feeds link as member", nil, func(tr tree) (Diff, error) {
			return tr.inv.RemoveGroupMember(t.Context(), id(), feeds(tr))
		}, ErrNotFound},
		{"store fails on create", storeErr, func(tr tree) (Diff, error) {
			return tr.inv.CreateSource(t.Context(), id(), Source{ID: id(), DeviceID: tr.device, Driver: "videoxlink"})
		}, storeErr},
		{"store fails on delete", storeErr, func(tr tree) (Diff, error) {
			return tr.inv.DeleteDevice(t.Context(), id(), tr.device)
		}, storeErr},
		{"store fails on update", storeErr, func(tr tree) (Diff, error) {
			return tr.inv.UpdateUserFields(t.Context(), id(), tr.port, UserFields{Label: "Cam 1"})
		}, storeErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTree(t)
			store := tr.store()
			store.err = tt.storeErr
			elements, sources, links := tr.inv.Elements(), tr.inv.Sources(), tr.inv.Links()

			if _, err := tt.run(tr); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.storeErr == nil && len(store.calls) != 0 {
				t.Errorf("store called: %v", store.calls)
			}
			if !reflect.DeepEqual(elements, tr.inv.Elements()) || !reflect.DeepEqual(sources, tr.inv.Sources()) || !reflect.DeepEqual(links, tr.inv.Links()) {
				t.Error("memory changed")
			}
		})
	}
}
