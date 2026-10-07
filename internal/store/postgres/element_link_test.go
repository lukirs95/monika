//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func TestDriverLinks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q := db.New(server.NewDB(t))
	device, source := newDeviceWithSource(t, q, "videoxlink")

	decoder, err := upsert(ctx, q, driverElement(device, source, "unit/D1"))
	if err != nil {
		t.Fatalf("upsert decoder: %v", err)
	}

	// Der Decoder wird von einem Remote-Encoder gespeist, den Monika noch nicht kennt.
	link := newID()
	var insertErr error
	q.InsertDriverLink(ctx, []db.InsertDriverLinkParams{{
		ID: link, Type: db.LinkTypeFeeds, SourceID: source, FromRef: new("xlink:E1"), ToID: &decoder,
	}}).Exec(func(_ int, err error) { insertErr = err })
	if insertErr != nil {
		t.Fatalf("InsertDriverLink: %v", insertErr)
	}

	// Das Remote-Gerät wird in Monika angelegt; sein Encoder trägt die global_ref.
	remote, remoteSource := newDeviceWithSource(t, q, "videoxlink")
	e1 := driverElement(remote, remoteSource, "unit/E1")
	e1.GlobalRef = new("xlink:E1")
	encoder, err := upsert(ctx, q, e1)
	if err != nil {
		t.Fatalf("upsert encoder: %v", err)
	}

	n, err := q.ResolveLinkRef(ctx, db.ResolveLinkRefParams{GlobalRef: "xlink:E1", ElementID: encoder})
	if err != nil || n != 1 {
		t.Fatalf("ResolveLinkRef = %d, %v; want 1, nil", n, err)
	}
	links, err := q.ListElementLinks(ctx)
	if err != nil || len(links) != 1 {
		t.Fatalf("ListElementLinks = %v, %v; want one link", links, err)
	}
	if l := links[0]; l.FromID == nil || *l.FromID != encoder || l.FromRef != nil {
		t.Fatalf("link = %+v, want from_id resolved and from_ref cleared", l)
	}

	n, err = q.DeleteDriverLinks(ctx, db.DeleteDriverLinksParams{SourceID: source, Ids: []uuid.UUID{link}})
	if err != nil || n != 1 {
		t.Fatalf("DeleteDriverLinks = %d, %v; want 1, nil", n, err)
	}
}

func newGroups(t *testing.T, q *db.Queries, n int) []uuid.UUID {
	t.Helper()
	groups := make([]uuid.UUID, n)
	for i := range groups {
		groups[i] = newID()
		if err := q.CreateGroup(t.Context(), db.CreateGroupParams{ID: groups[i]}); err != nil {
			t.Fatalf("CreateGroup: %v", err)
		}
	}
	return groups
}

func addMember(ctx context.Context, pool *pgxpool.Pool, group, member uuid.UUID) error {
	return postgres.New(pool).AddGroupMember(ctx, inventory.Link{ID: newID(), Type: inventory.LinkMember, FromID: group, ToID: member})
}

func TestMemberLinkWouldCycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	q := db.New(pool)

	// Bühne ⊃ Stagebox ⊃ Kanalgruppe
	g := newGroups(t, q, 3)
	stage, box, channels := g[0], g[1], g[2]
	for _, m := range []struct{ group, member uuid.UUID }{{stage, box}, {box, channels}} {
		if err := addMember(ctx, pool, m.group, m.member); err != nil {
			t.Fatalf("AddGroupMember: %v", err)
		}
	}

	tests := []struct {
		name          string
		group, member uuid.UUID
		want          bool
	}{
		{"self", stage, stage, true},
		{"direct parent as member", box, stage, true},
		{"ancestor as member", channels, stage, true},
		{"descendant as member", stage, channels, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := q.MemberLinkWouldCycle(ctx, db.MemberLinkWouldCycleParams{GroupID: tt.group, MemberID: tt.member})
			if err != nil || got != tt.want {
				t.Fatalf("MemberLinkWouldCycle = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestAddGroupMemberRejectsCycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	g := newGroups(t, db.New(pool), 2)

	if err := addMember(ctx, pool, g[0], g[1]); err != nil {
		t.Fatalf("AddGroupMember: %v", err)
	}
	if err := addMember(ctx, pool, g[1], g[0]); !errors.Is(err, inventory.ErrMemberCycle) {
		t.Fatalf("AddGroupMember = %v, want ErrMemberCycle", err)
	}
}

// Eine Transaktion hält den Lock und hat A ⊃ B eingefügt, aber noch nicht committet.
// Die gegenläufige Änderung B ⊃ A muss warten und erkennt danach den Zyklus. Ohne Lock
// sähe sie den Link noch nicht und würde den Zyklus anlegen.
func TestAddGroupMemberWaitsForLock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	g := newGroups(t, db.New(pool), 2)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	qtx := db.New(tx)
	if err := qtx.LockMemberLinks(ctx); err != nil {
		t.Fatalf("LockMemberLinks: %v", err)
	}
	if err := qtx.CreateMemberLink(ctx, db.CreateMemberLinkParams{ID: newID(), GroupID: g[0], MemberID: g[1]}); err != nil {
		t.Fatalf("CreateMemberLink: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- addMember(ctx, pool, g[1], g[0]) }()
	select {
	case err := <-done:
		t.Fatalf("AddGroupMember = %v without waiting for the lock", err)
	case <-time.After(200 * time.Millisecond):
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-done; !errors.Is(err, inventory.ErrMemberCycle) {
		t.Fatalf("AddGroupMember = %v, want ErrMemberCycle", err)
	}
}
