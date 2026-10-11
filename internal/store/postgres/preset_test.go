//go:build integration

package postgres_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/preset"
	"github.com/lukirs95/monika/internal/store/postgres"
)

// presetFixture legt ein Gerät mit einem Port und eine Gruppe an.
func presetFixture(t *testing.T, s *postgres.Store) (device, port, group uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	device, source, port, group := newID(), newID(), newID(), newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := s.CreateSource(ctx, uuid.Nil, inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink"}, newID()); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if _, err := s.ApplyDiscovery(ctx, inventory.Discovery{SourceID: source, Upsert: []inventory.Element{{
		ID: port, Kind: "sender", ParentID: device, DeviceID: device, SourceID: source, ExternalID: "unit/E1",
	}}, Seen: []uuid.UUID{port}}); err != nil {
		t.Fatalf("ApplyDiscovery: %v", err)
	}
	if err := s.CreateGroup(ctx, uuid.Nil, inventory.Element{ID: group}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	return device, port, group
}

func step(stage int, target uuid.UUID, action string) preset.Step {
	return preset.Step{ID: newID(), Stage: stage, Position: 1, ElementID: target, Action: action}
}

func TestPresets(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)
	device, port, group := presetFixture(t, s)

	stop := step(1, group, "stop")
	set := step(2, port, "set")
	set.Params = json.RawMessage(`{"bitrate": 25}`)
	start := step(3, group, "start")
	start.Delay = 5 * time.Second
	reboot := step(3, device, "reboot")
	reboot.Position, reboot.DeviceLevel, reboot.Driver, reboot.ContinueOnError = 2, true, "videoxlink", true

	p := preset.Preset{ID: newID(), Name: "Produktion XY", CreatedByID: user, Steps: []preset.Step{stop, set, start, reboot}}
	if err := s.CreatePreset(ctx, user, p); err != nil {
		t.Fatalf("CreatePreset: %v", err)
	}
	got, err := s.GetPreset(ctx, p.ID)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("GetPreset = %+v, %v;\nwant %+v", got, err, p)
	}

	t.Run("update keeps step IDs", func(t *testing.T) {
		changed := p
		set.Params = json.RawMessage(`{"bitrate": 20}`)
		added := step(4, port, "start")
		changed.Name = "Produktion XY – 20 Mbit"
		changed.Steps = []preset.Step{set, start, reboot, added} // stop entfällt
		if err := s.UpdatePreset(ctx, user, changed); err != nil {
			t.Fatalf("UpdatePreset: %v", err)
		}
		got, err := s.GetPreset(ctx, p.ID)
		if err != nil || !reflect.DeepEqual(got, changed) {
			t.Fatalf("GetPreset = %+v, %v;\nwant %+v", got, err, changed)
		}
		list, err := s.ListPresets(ctx)
		if err != nil || len(list) != 1 || list[0].Name != changed.Name || list[0].Steps != nil {
			t.Fatalf("ListPresets = %+v, %v; want metadata of one preset", list, err)
		}
	})

	t.Run("invalid steps", func(t *testing.T) {
		other := preset.Preset{ID: newID(), Name: "Anderes", Steps: []preset.Step{step(1, port, "start")}}
		if err := s.CreatePreset(ctx, user, other); err != nil {
			t.Fatalf("CreatePreset other: %v", err)
		}
		cases := map[string]preset.Step{
			"unknown target":         step(1, newID(), "start"),
			"step of another preset": other.Steps[0],
		}
		for name, st := range cases {
			bad := preset.Preset{ID: newID(), Name: name, Steps: []preset.Step{st}}
			if err := s.CreatePreset(ctx, user, bad); !errors.Is(err, preset.ErrInvalidTarget) {
				t.Errorf("%s: CreatePreset = %v, want ErrInvalidTarget", name, err)
			}
		}
	})

	t.Run("delete", func(t *testing.T) {
		if err := s.DeletePreset(ctx, user, p.ID); err != nil {
			t.Fatalf("DeletePreset: %v", err)
		}
		if _, err := s.GetPreset(ctx, p.ID); !errors.Is(err, preset.ErrNotFound) {
			t.Errorf("GetPreset after delete = %v, want ErrNotFound", err)
		}
		if err := s.DeletePreset(ctx, user, p.ID); !errors.Is(err, preset.ErrNotFound) {
			t.Errorf("DeletePreset again = %v, want ErrNotFound", err)
		}
	})

	// Audit: Vorher/Nachher enthalten die Schritte.
	var verbs []string
	for _, e := range auditLog(t, pool) {
		if e.ObjectType != "preset" || e.ObjectID != p.ID {
			continue
		}
		verbs = append(verbs, e.Verb)
		if e.Verb == "update" && (!strings.Contains(string(e.Before), stop.ID.String()) || strings.Contains(string(e.After), stop.ID.String())) {
			t.Errorf("update entry does not show the removed step %s", stop.ID)
		}
	}
	if strings.Join(verbs, ",") != "create,update,delete" {
		t.Errorf("preset audit = %v, want create,update,delete", verbs)
	}
}

func TestPresetRefusesDeletedTargets(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := postgres.New(server.NewDB(t))
	device, port, group := presetFixture(t, s)
	if err := s.DeleteDevice(ctx, uuid.Nil, device); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if err := s.DeleteGroup(ctx, uuid.Nil, group); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	for name, target := range map[string]uuid.UUID{"deleted device": device, "element of deleted device": port, "deleted group": group} {
		p := preset.Preset{ID: newID(), Name: name, Steps: []preset.Step{step(1, target, "start")}}
		if err := s.CreatePreset(ctx, uuid.Nil, p); !errors.Is(err, preset.ErrInvalidTarget) {
			t.Errorf("%s: CreatePreset = %v, want ErrInvalidTarget", name, err)
		}
	}
}

// Eine Transaktion hält das Gerät wie DeleteDevice (FOR UPDATE) und löscht es. Das gleichzeitige
// Anlegen eines Presets auf einen Port des Geräts muss warten und danach ablehnen.
func TestPresetWaitsForDeviceDeletion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	device, port, _ := presetFixture(t, s)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM element WHERE id = $1 FOR UPDATE`, device); err != nil {
		t.Fatalf("lock device: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- s.CreatePreset(ctx, uuid.Nil, preset.Preset{ID: newID(), Name: "Race", Steps: []preset.Step{step(1, port, "start")}})
	}()
	select {
	case err := <-done:
		t.Fatalf("CreatePreset = %v without waiting for the device lock", err)
	case <-time.After(200 * time.Millisecond):
	}

	if _, err := tx.Exec(ctx, `DELETE FROM element WHERE id = $1`, device); err != nil {
		t.Fatalf("delete device: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-done; !errors.Is(err, preset.ErrInvalidTarget) {
		t.Fatalf("CreatePreset = %v, want ErrInvalidTarget", err)
	}
}
