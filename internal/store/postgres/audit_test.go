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

func TestAuditDelete(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)

	device, source, port, group := newID(), newID(), newID(), newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := s.CreateSource(ctx, uuid.Nil, inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink"}, newID()); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if _, err := s.ApplyDiscovery(ctx, inventory.Discovery{SourceID: source, Upsert: []inventory.Element{{
		ID: port, Kind: "port", ParentID: device, DeviceID: device, SourceID: source, ExternalID: "sdi/1",
	}}, Seen: []uuid.UUID{port}}); err != nil {
		t.Fatalf("ApplyDiscovery: %v", err)
	}
	if err := s.CreateGroup(ctx, uuid.Nil, inventory.Element{ID: group}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	membership := newID()
	if err := s.AddGroupMember(ctx, uuid.Nil, inventory.Link{ID: membership, Type: inventory.LinkMember, FromID: group, ToID: port}); err != nil {
		t.Fatalf("AddGroupMember: %v", err)
	}
	deviceRule, groupRule := newID(), newID()
	for _, r := range [][2]uuid.UUID{{deviceRule, port}, {groupRule, group}} {
		if _, err := pool.Exec(ctx, `INSERT INTO rule (id, name, fact, op, value, scope_element_id) VALUES ($1, 'r', 'running', 'eq', 'true', $2)`, r[0], r[1]); err != nil {
			t.Fatalf("insert rule: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `TRUNCATE audit_log`); err != nil {
		t.Fatalf("truncate audit_log: %v", err)
	}

	if err := s.DeleteDevice(ctx, user, device); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if err := s.DeleteGroup(ctx, user, group); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	got := []string{}
	for _, e := range auditLog(t, pool) {
		if e.Actor == nil || *e.Actor != user || e.Verb != "delete" {
			t.Errorf("entry %+v, want delete by %s", e, user)
		}
		got = append(got, e.ObjectType+" "+e.ObjectID.String())
	}
	// Ein Eintrag pro Gerät bzw. Gruppe, nicht pro Element; Mitgelöschtes je mit eigenem Eintrag.
	want := []string{
		"element " + device.String(),
		"element_link " + membership.String(),
		"rule " + deviceRule.String(),
		"element " + group.String(),
		"rule " + groupRule.String(),
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("audit entries:\n got  %v\n want %v", got, want)
	}
}

func TestAuditDiscovery(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)

	device, source, port, peer := newID(), newID(), newID(), newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := s.CreateSource(ctx, uuid.Nil, inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink"}, newID()); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	element := func(id uuid.UUID, kind inventory.Kind, ext string) inventory.Element {
		return inventory.Element{ID: id, Kind: kind, ParentID: device, DeviceID: device, SourceID: source, ExternalID: ext}
	}
	if err := s.CreatePlannedElement(ctx, uuid.Nil, element(peer, "peer", "peer/X8A9001")); err != nil {
		t.Fatalf("CreatePlannedElement: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE audit_log`); err != nil {
		t.Fatalf("truncate audit_log: %v", err)
	}

	link := inventory.Link{ID: newID(), Type: inventory.LinkUses, SourceID: source, FromID: port, ToID: peer}
	runs := []inventory.Discovery{
		// Lauf 1: Port ist neu, der vorab angelegte Peer erscheint, ein Link entsteht.
		{SourceID: source, Upsert: []inventory.Element{element(port, "port", "sdi/1"), element(peer, "peer", "peer/X8A9001")},
			Seen: []uuid.UUID{port, peer}, AddLinks: []inventory.Link{link}},
		// Lauf 2: Peer verschwindet, der Link fällt weg.
		{SourceID: source, Seen: []uuid.UUID{port}, RemoveLinks: []uuid.UUID{link.ID}},
		// Lauf 3: Peer kehrt zurück, ohne geändert zu sein (nur in Seen).
		{SourceID: source, Seen: []uuid.UUID{port, peer}},
	}
	for i, d := range runs {
		if _, err := s.ApplyDiscovery(ctx, d); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	got := []string{}
	for _, e := range auditLog(t, pool) {
		if e.Actor != nil {
			t.Errorf("entry %+v has actor, want Monika (NULL)", e)
		}
		entry := e.Verb + " " + e.ObjectType + " " + e.ObjectID.String()
		if e.Verb == "update" {
			entry += " " + string(e.Before) + "→" + string(e.After)
		}
		got = append(got, entry)
	}
	want := []string{
		"create element " + port.String(),
		`update element ` + peer.String() + ` {"present": false}→{"present": true}`,
		"create element_link " + link.ID.String(),
		"delete element_link " + link.ID.String(),
		`update element ` + peer.String() + ` {"present": true}→{"present": false}`,
		`update element ` + peer.String() + ` {"present": false}→{"present": true}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("audit entries:\n got  %q\n want %q", got, want)
	}
}
