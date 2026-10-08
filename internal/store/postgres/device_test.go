//go:build integration

package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
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

	if err := postgres.New(pool).DeleteDevice(ctx, uuid.Nil, device); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	if e := element(t, q, device); e.DeletedAt == nil || e.Monitored {
		t.Fatalf("device = %+v, want deleted_at set and not monitored", e)
	}
	sources, err := q.ListDeviceSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].Enabled {
		t.Fatalf("ListDeviceSources = %+v, %v; want source disabled", sources, err)
	}
	// Das Element verschwindet; sein eigenes monitored bleibt, stillgelegt ist es über das Gerät.
	if e := element(t, q, port); e.Present || !e.Monitored {
		t.Fatalf("element = %+v, want missing with own monitored flag kept", e)
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
	if err := postgres.New(pool).DeleteDevice(ctx, uuid.Nil, device); err != nil {
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

	err = postgres.New(pool).DeleteDevice(ctx, uuid.Nil, device)
	var inUse *inventory.InUseError
	if !errors.As(err, &inUse) || len(inUse.Presets) != 1 || inUse.Presets[0].ID != preset {
		t.Fatalf("DeleteDevice = %v, want InUseError naming preset %s", err, preset)
	}
	if e := element(t, q, port); !e.Monitored {
		t.Fatalf("element %s no longer monitored after refused delete", port)
	}
}

func TestDeleteDeviceNotFound(t *testing.T) {
	t.Parallel()
	pool := server.NewDB(t)

	if err := postgres.New(pool).DeleteDevice(t.Context(), uuid.Nil, uuid.Must(uuid.NewV7())); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatalf("DeleteDevice = %v, want ErrNotFound", err)
	}
}

func TestDeleteDeviceRemovesScopedConfig(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	q := db.New(pool)

	device, source := newDeviceWithSource(t, q, "videoxlink")
	port, err := upsert(ctx, q, driverElement(device, source, "sdi/1"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	scoped, global, role := newID(), newID(), newID()
	setup := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO rule (id, name, fact, op, value, scope_element_id) VALUES ($1, 'Signal', 'signal_present', 'eq', 'true', $2)`, []any{scoped, port}},
		{`INSERT INTO rule (id, name, fact, op, value) VALUES ($1, 'Temperatur', 'vxl.cpu_temp', 'lte', '80')`, []any{global}},
		{`INSERT INTO finding (element_id, origin, rule_id, code, severity, raised_at) VALUES ($1, 'rule', $2, 'rule.violated', 'error', now())`, []any{port, scoped}},
		{`INSERT INTO role (id, name) VALUES ($1, 'Operator')`, []any{role}},
		{`INSERT INTO permission_grant (role_id, permission, scope_element_id) VALUES ($1, 'operate', $2)`, []any{role, device}},
		{`INSERT INTO forward_target (name, kind, events, scope_element_id) VALUES ('hook', 'webhook', '{}', $1)`, []any{port}},
	}
	for _, st := range setup {
		if _, err := pool.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("%s: %v", st.sql, err)
		}
	}

	if err := postgres.New(pool).DeleteDevice(ctx, uuid.Nil, device); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	checks := []struct {
		name string
		sql  string
		args []any
		want int
	}{
		{"scoped rule deleted", `SELECT count(*) FROM rule WHERE id = $1`, []any{scoped}, 0},
		{"global rule kept", `SELECT count(*) FROM rule WHERE id = $1`, []any{global}, 1},
		{"permission grant deleted", `SELECT count(*) FROM permission_grant`, nil, 0},
		{"forward target deleted", `SELECT count(*) FROM forward_target`, nil, 0},
		{"no active finding", `SELECT count(*) FROM finding WHERE cleared_at IS NULL`, nil, 0},
		{"finding withdrawn", `SELECT count(*) FROM finding WHERE details->>'cleared_reason' = 'device_deleted' AND rule_id IS NULL`, nil, 1},
	}
	for _, c := range checks {
		var got int
		if err := pool.QueryRow(ctx, c.sql, c.args...).Scan(&got); err != nil || got != c.want {
			t.Errorf("%s: count = %d, %v; want %d", c.name, got, err, c.want)
		}
	}
}
