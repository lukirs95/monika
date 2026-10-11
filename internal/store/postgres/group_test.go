//go:build integration

package postgres_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func TestDeleteGroup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	q := db.New(pool)
	s := postgres.New(pool)

	device, source := newDeviceWithSource(t, q, "videoxlink")
	port, err := upsert(ctx, q, driverElement(device, source, "sdi/1"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Bühne ⊃ Stagebox ⊃ Port
	g := newGroups(t, q, 2)
	stage, box := g[0], g[1]
	for _, m := range [][2]any{{stage, box}, {box, port}} {
		if _, err := pool.Exec(ctx, `INSERT INTO element_link (id, type, from_id, to_id) VALUES ($1, 'member', $2, $3)`, newID(), m[0], m[1]); err != nil {
			t.Fatalf("insert member link: %v", err)
		}
	}
	boxRule, stageRule, role := newID(), newID(), newID()
	setup := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO rule (id, name, fact, op, value, scope_element_id) VALUES ($1, 'Signal', 'signal_present', 'eq', 'true', $2)`, []any{boxRule, box}},
		{`INSERT INTO rule (id, name, fact, op, value, scope_element_id) VALUES ($1, 'Da', 'present', 'eq', 'true', $2)`, []any{stageRule, stage}},
		{`INSERT INTO finding (element_id, element_path, origin, rule_id, code, severity, raised_at) VALUES ($1, 'X8 / SDI 1', 'rule', $2, 'rule.violated', 'error', now())`, []any{port, boxRule}},
		{`INSERT INTO role (id, name) VALUES ($1, 'Operator')`, []any{role}},
		{`INSERT INTO permission_grant (role_id, permission, scope_element_id) VALUES ($1, 'operate', $2)`, []any{role, box}},
		{`INSERT INTO forward_target (name, kind, events, scope_element_id) VALUES ('hook', 'webhook', '{}', $1)`, []any{box}},
	}
	for _, st := range setup {
		if _, err := pool.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("%s: %v", st.sql, err)
		}
	}

	if err := s.DeleteGroup(ctx, uuid.Nil, box); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	checks := []struct {
		name string
		sql  string
		args []any
		want int
	}{
		{"group deleted", `SELECT count(*) FROM element WHERE id = $1`, []any{box}, 0},
		{"memberships removed in both directions", `SELECT count(*) FROM element_link WHERE from_id = $1 OR to_id = $1`, []any{box}, 0},
		{"member kept", `SELECT count(*) FROM element WHERE id = $1`, []any{port}, 1},
		{"rule of group deleted", `SELECT count(*) FROM rule WHERE id = $1`, []any{boxRule}, 0},
		{"rule of parent group kept", `SELECT count(*) FROM rule WHERE id = $1`, []any{stageRule}, 1},
		{"finding at member withdrawn", `SELECT count(*) FROM finding WHERE cleared_at IS NOT NULL AND details->>'cleared_reason' = 'rule_deleted'`, nil, 1},
		{"permission grant deleted", `SELECT count(*) FROM permission_grant`, nil, 0},
		{"forward target deleted", `SELECT count(*) FROM forward_target`, nil, 0},
	}
	for _, c := range checks {
		var got int
		if err := pool.QueryRow(ctx, c.sql, c.args...).Scan(&got); err != nil || got != c.want {
			t.Errorf("%s: count = %d, %v; want %d", c.name, got, err, c.want)
		}
	}
	if err := s.DeleteGroup(ctx, uuid.Nil, box); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatalf("DeleteGroup again = %v, want ErrNotFound", err)
	}
}

func TestDeleteGroupRefused(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	q := db.New(pool)
	s := postgres.New(pool)

	group := newGroups(t, q, 1)[0]
	preset := newID()
	if _, err := pool.Exec(ctx, `INSERT INTO preset (id, name) VALUES ($1, 'Show Start')`, preset); err != nil {
		t.Fatalf("insert preset: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO preset_step (preset_id, stage, position, element_id, action) VALUES ($1, 1, 1, $2, 'start')`,
		preset, group); err != nil {
		t.Fatalf("insert preset_step: %v", err)
	}
	var inUse *inventory.InUseError
	if err := s.DeleteGroup(ctx, uuid.Nil, group); !errors.As(err, &inUse) || len(inUse.Presets) != 1 || inUse.Presets[0].ID != preset {
		t.Fatalf("DeleteGroup = %v, want InUseError naming preset %s", err, preset)
	}

	device, _ := newDeviceWithSource(t, q, "videoxlink")
	if err := s.DeleteGroup(ctx, uuid.Nil, device); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatalf("DeleteGroup on a device = %v, want ErrNotFound", err)
	}
}
