//go:build integration

package postgres_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestAccessElement(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := postgres.New(server.NewDB(t))

	device, source, access := newID(), newID(), newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device, Label: "Lokal 1"}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := s.CreateSource(ctx, uuid.Nil, inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink", Enabled: true}, access); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	snap := load(t, s)
	got := findElement(t, snap, access)
	if got.Kind != inventory.KindAccess || got.ParentID != device || got.SourceID != source ||
		got.ExternalID != "" || got.Name != "videoxlink" || !got.Present {
		t.Fatalf("access element = %+v, want access of source %s under device %s", got, source, device)
	}

	t.Run("driver fills it via empty external_id", func(t *testing.T) {
		root := inventory.Element{
			ID: newID(), Kind: inventory.KindAccess, ParentID: device, DeviceID: device, SourceID: source,
			Name: "XLink-API", Actions: []string{"reboot"}, CriticalActions: []string{"reboot"},
		}
		// Die Discovery muss die bekannte ID des Zugangs-Elements verwenden.
		if _, err := s.ApplyDiscovery(ctx, inventory.Discovery{SourceID: source, Upsert: []inventory.Element{root}}); err == nil {
			t.Fatal("ApplyDiscovery with unknown id succeeded, want id mismatch error")
		}
		// Der Lauf meldet das Zugangs-Element nicht in Seen; es verschwindet trotzdem nicht.
		root.ID = access
		if _, err := s.ApplyDiscovery(ctx, inventory.Discovery{SourceID: source, Upsert: []inventory.Element{root}}); err != nil {
			t.Fatalf("ApplyDiscovery: %v", err)
		}
		got := findElement(t, load(t, s), access)
		if !got.Present || got.LastSeenAt.IsZero() || !slices.Equal(got.Actions, []string{"reboot"}) {
			t.Fatalf("access element = %+v, want present, seen, actions {reboot}", got)
		}
	})

	t.Run("refused for unknown and deleted devices", func(t *testing.T) {
		if err := s.CreateSource(ctx, uuid.Nil, inventory.Source{ID: newID(), DeviceID: newID(), Driver: "ipmi"}, newID()); !errors.Is(err, inventory.ErrNotFound) {
			t.Fatalf("CreateSource on unknown device = %v, want ErrNotFound", err)
		}
		if err := s.DeleteDevice(ctx, uuid.Nil, device); err != nil {
			t.Fatalf("DeleteDevice: %v", err)
		}
		if err := s.CreateSource(ctx, uuid.Nil, inventory.Source{ID: newID(), DeviceID: device, Driver: "ipmi"}, newID()); !errors.Is(err, inventory.ErrNotFound) {
			t.Fatalf("CreateSource on deleted device = %v, want ErrNotFound", err)
		}
	})
}

func load(t *testing.T, s *postgres.Store) inventory.Snapshot {
	t.Helper()
	snap, err := s.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return snap
}
