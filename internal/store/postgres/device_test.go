//go:build integration

package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/store/postgres"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func TestDeleteDevice(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	q := db.New(pool)

	device, source := newDeviceWithSource(t, q, "videoxlink")
	port, err := upsert(ctx, q, driverElement(device, source, "sdi/1"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	group := newGroups(t, q, 1)[0]
	if err := addMember(ctx, pool, group, port); err != nil {
		t.Fatalf("AddGroupMember: %v", err)
	}

	if err := postgres.DeleteDevice(ctx, pool, device); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	devices, err := q.ListDevices(ctx)
	if err != nil || len(devices) != 1 || devices[0].DeletedAt == nil {
		t.Fatalf("ListDevices = %+v, %v; want device with deleted_at", devices, err)
	}
	sources, err := q.ListDeviceSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].Enabled {
		t.Fatalf("ListDeviceSources = %+v, %v; want source disabled", sources, err)
	}
	if e := element(t, q, port); e.Monitored {
		t.Fatalf("element %s still monitored", port)
	}
	links, err := q.ListElementLinks(ctx)
	if err != nil || len(links) != 0 {
		t.Fatalf("ListElementLinks = %+v, %v; want group membership removed", links, err)
	}

	// Quellen gelöschter Geräte lassen sich nicht wieder aktivieren.
	n, err := q.UpdateDeviceSource(ctx, db.UpdateDeviceSourceParams{ID: source, Config: json.RawMessage(`{}`), Enabled: true})
	if err != nil || n != 0 {
		t.Fatalf("UpdateDeviceSource = %d, %v; want 0, nil", n, err)
	}
	if err := postgres.DeleteDevice(ctx, pool, device); err != nil {
		t.Fatalf("DeleteDevice again: %v", err)
	}
}

func TestDeleteDeviceUsedByPreset(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	q := db.New(pool)

	device, source := newDeviceWithSource(t, q, "videoxlink")
	port, err := upsert(ctx, q, driverElement(device, source, "sdi/1"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	preset := newID()
	if _, err := pool.Exec(ctx, `INSERT INTO preset (id, name) VALUES ($1, 'Show Start')`, preset); err != nil {
		t.Fatalf("insert preset: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO preset_step (preset_id, stage, position, element_id, action) VALUES ($1, 1, 1, $2, 'start')`,
		preset, port); err != nil {
		t.Fatalf("insert preset_step: %v", err)
	}

	err = postgres.DeleteDevice(ctx, pool, device)
	var inUse *postgres.DeviceInUseError
	if !errors.As(err, &inUse) || len(inUse.Presets) != 1 || inUse.Presets[0].ID != preset {
		t.Fatalf("DeleteDevice = %v, want DeviceInUseError naming preset %s", err, preset)
	}
	if e := element(t, q, port); !e.Monitored {
		t.Fatalf("element %s no longer monitored after refused delete", port)
	}
}

func TestDeleteDeviceNotFound(t *testing.T) {
	t.Parallel()
	pool := server.NewDB(t)

	if err := postgres.DeleteDevice(t.Context(), pool, uuid.Must(uuid.NewV7())); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("DeleteDevice = %v, want ErrNotFound", err)
	}
}
