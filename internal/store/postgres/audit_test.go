//go:build integration

package postgres_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
)

// auditEntry ist ein Eintrag aus audit_log, reduziert auf das, was Tests vergleichen.
type auditEntry struct {
	Actor      *uuid.UUID
	Verb       string
	ObjectType string
	ObjectID   uuid.UUID
	Before     json.RawMessage
	After      json.RawMessage
}

func auditLog(t *testing.T, pool *pgxpool.Pool) []auditEntry {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT actor_id, verb, object_type, object_id, before, after FROM audit_log ORDER BY at, id`)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[auditEntry])
	if err != nil {
		t.Fatalf("collect audit_log: %v", err)
	}
	return entries
}

func newUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := newID()
	if _, err := pool.Exec(t.Context(), `INSERT INTO app_user (id, username) VALUES ($1, $2)`, id, id.String()); err != nil {
		t.Fatalf("insert app_user: %v", err)
	}
	return id
}

func TestAuditUserChanges(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)

	device, source, group, link := newID(), newID(), newID(), newID()
	steps := []struct {
		name string
		do   func() error
	}{
		{"create device", func() error { return s.CreateDevice(ctx, user, inventory.Element{ID: device, Label: "Lokal 1"}) }},
		{"create source", func() error {
			return s.CreateSource(ctx, user, inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink", Enabled: true}, newID())
		}},
		{"update source", func() error {
			return s.UpdateSource(ctx, user, inventory.Source{ID: source, Address: "10.0.0.5", Enabled: true})
		}},
		{"rename device", func() error {
			return s.UpdateUserFields(ctx, user, device, inventory.UserFields{Label: "Lokal 2", Monitored: true})
		}},
		{"create group", func() error { return s.CreateGroup(ctx, user, inventory.Element{ID: group, Label: "Bühne"}) }},
		{"add member", func() error {
			return s.AddGroupMember(ctx, user, inventory.Link{ID: link, Type: inventory.LinkMember, FromID: group, ToID: device})
		}},
		{"update member", func() error {
			return s.UpdateGroupMember(ctx, user, inventory.Link{ID: link, Label: "Kamera 1"})
		}},
		{"remove member", func() error { return s.RemoveGroupMember(ctx, user, link) }},
	}
	for _, st := range steps {
		if err := st.do(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
	}

	type key struct {
		Verb, ObjectType string
		ObjectID         uuid.UUID
	}
	want := []key{
		{"create", "element", device},
		{"create", "device_source", source},
		{"update", "device_source", source},
		{"update", "element", device},
		{"create", "element", group},
		{"create", "element_link", link},
		{"update", "element_link", link},
		{"delete", "element_link", link},
	}
	entries := auditLog(t, pool)
	got := make([]key, len(entries))
	for i, e := range entries {
		got[i] = key{e.Verb, e.ObjectType, e.ObjectID}
		if e.Actor == nil || *e.Actor != user {
			t.Errorf("entry %d (%s %s): actor = %v, want %s", i, e.Verb, e.ObjectType, e.Actor, user)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("audit entries:\n got  %v\n want %v", got, want)
	}

	// Beim Umbenennen stehen nur die User-Spalten in Vorher/Nachher.
	var before, after struct{ Label string }
	rename := entries[3]
	if err := json.Unmarshal(rename.Before, &before); err != nil || before.Label != "Lokal 1" {
		t.Errorf("rename before = %s, %v; want label Lokal 1", rename.Before, err)
	}
	if err := json.Unmarshal(rename.After, &after); err != nil || after.Label != "Lokal 2" {
		t.Errorf("rename after = %s, %v; want label Lokal 2", rename.After, err)
	}
}
