//go:build integration

package postgres_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestStoreApplyDiscovery(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := postgres.New(server.NewDB(t))

	device, source := newID(), newID()
	if err := s.CreateDevice(ctx, inventory.Element{ID: device, Label: "Lokal 1"}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := s.CreateSource(ctx, inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink", Enabled: true}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}

	encoder := inventory.Element{
		ID: newID(), Kind: "sender", ParentID: device, DeviceID: device, SourceID: source,
		ExternalID: "unit/E1", GlobalRef: "xlink:E1", Transport: "xlink", Direction: inventory.DirectionOut,
		Name: "E1", Attributes: json.RawMessage(`{"slot": 1}`), Position: new(1),
		Actions: []string{"start", "stop"}, CriticalActions: []string{}, Settings: json.RawMessage(`{"bitrate": {"type": "int"}}`),
		Present: true, Monitored: true,
	}
	port := inventory.Element{
		ID: newID(), Kind: "port", ParentID: device, DeviceID: device, SourceID: source,
		ExternalID: "sdi/1", Attributes: json.RawMessage(`{}`), Actions: []string{}, Present: true, Monitored: true,
	}
	// Der Link verweist per global_ref auf den Encoder aus demselben Lauf und wird sofort aufgelöst.
	link := inventory.Link{ID: newID(), Type: inventory.LinkFeeds, SourceID: source, FromID: port.ID, ToRef: "xlink:E1"}

	missing, err := s.ApplyDiscovery(ctx, inventory.Discovery{
		SourceID: source,
		Identity: &inventory.Identity{Vendor: "VideoXLink", Model: "X8"},
		Upsert:   []inventory.Element{encoder, port},
		Seen:     []uuid.UUID{encoder.ID, port.ID},
		AddLinks: []inventory.Link{link},
	})
	if err != nil || len(missing) != 0 {
		t.Fatalf("ApplyDiscovery = %v, %v; want no missing elements", missing, err)
	}

	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(snap.Sources) != 1 || snap.Sources[0].Identity.Model != "X8" {
		t.Fatalf("sources = %+v, want identity X8", snap.Sources)
	}
	got := findElement(t, snap, encoder.ID)
	if got.FirstSeenAt.IsZero() || got.LastSeenAt.IsZero() {
		t.Fatalf("encoder = %+v, want first and last seen set", got)
	}
	got.FirstSeenAt, got.LastSeenAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, encoder) {
		t.Fatalf("encoder after round trip:\n got  %+v\n want %+v", got, encoder)
	}
	want := link
	want.ToID, want.ToRef = encoder.ID, ""
	if len(snap.Links) != 1 || snap.Links[0] != want {
		t.Fatalf("links = %+v, want %+v", snap.Links, want)
	}

	// Zweiter Lauf: der Encoder fehlt, der Link ist weg.
	missing, err = s.ApplyDiscovery(ctx, inventory.Discovery{
		SourceID:    source,
		Seen:        []uuid.UUID{port.ID},
		RemoveLinks: []uuid.UUID{link.ID},
	})
	if err != nil || !slices.Equal(missing, []uuid.UUID{encoder.ID}) {
		t.Fatalf("ApplyDiscovery = %v, %v; want encoder missing", missing, err)
	}
	snap, err = s.Load(ctx)
	if err != nil || len(snap.Links) != 0 || findElement(t, snap, encoder.ID).Present {
		t.Fatalf("Load = %+v, %v; want encoder missing and no links", snap, err)
	}
}

func findElement(t *testing.T, snap inventory.Snapshot, id uuid.UUID) inventory.Element {
	t.Helper()
	i := slices.IndexFunc(snap.Elements, func(e inventory.Element) bool { return e.ID == id })
	if i < 0 {
		t.Fatalf("element %s not in snapshot", id)
	}
	return snap.Elements[i]
}
