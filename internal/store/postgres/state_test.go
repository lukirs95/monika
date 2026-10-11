//go:build integration

package postgres_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestSaveLoadHealth(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)

	device, group := newID(), newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := s.CreateGroup(ctx, uuid.Nil, inventory.Element{ID: group}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	want := []evaluation.ElementHealth{
		{ElementID: device, Health: evaluation.HealthError, Rollup: &evaluation.Rollup{OK: 12, Error: 1, Unchecked: 40, Unmonitored: 2}},
		// Ohne Health und ohne Rollup: beides wird als NULL gespeichert.
		{ElementID: group, Health: evaluation.HealthNone},
	}
	if err := s.SaveHealth(ctx, want); err != nil {
		t.Fatalf("SaveHealth: %v", err)
	}
	got, err := s.LoadHealth(ctx)
	slices.SortFunc(got, func(a, b evaluation.ElementHealth) int { return slices.Compare(a.ElementID[:], b.ElementID[:]) })
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadHealth = %+v, %v; want %+v", got, err, want)
	}

	updatedAt := func() time.Time {
		t.Helper()
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM element_state WHERE element_id = $1`, device).Scan(&at); err != nil {
			t.Fatalf("updated_at: %v", err)
		}
		return at
	}
	first := updatedAt()

	// Derselbe Wert erneut: die Zeile bleibt unberührt.
	if err := s.SaveHealth(ctx, want[:1]); err != nil {
		t.Fatalf("SaveHealth unchanged: %v", err)
	}
	if !updatedAt().Equal(first) {
		t.Fatal("unchanged health moved updated_at")
	}

	// Geänderter Rollup: die Zeile wird geschrieben.
	changed := want[0]
	changed.Rollup = &evaluation.Rollup{OK: 13, Unmonitored: 2}
	changed.Health = evaluation.HealthOK
	if err := s.SaveHealth(ctx, []evaluation.ElementHealth{changed}); err != nil {
		t.Fatalf("SaveHealth changed: %v", err)
	}
	if !updatedAt().After(first) {
		t.Fatal("changed health did not move updated_at")
	}
}
