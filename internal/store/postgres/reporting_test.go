//go:build integration

package postgres_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
)

// Die Views im Schema reporting liefern Pfade ohne Zugangs-Element und halten Befunde gelöschter
// Elemente über den gespeicherten Pfad lesbar.
func TestReportingViews(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)

	device, source, access, unit := newID(), newID(), newID(), newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device, Label: "L1"}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := s.CreateSource(ctx, uuid.Nil, inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink"}, access); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if _, err := s.ApplyDiscovery(ctx, inventory.Discovery{
		SourceID: source,
		Upsert:   []inventory.Element{{ID: unit, Kind: "receiver", ParentID: access, DeviceID: device, SourceID: source, ExternalID: "D1", Name: "D1"}},
		Seen:     []uuid.UUID{unit},
	}); err != nil {
		t.Fatalf("ApplyDiscovery: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO finding (element_id, element_path, origin, code, severity, raised_at) VALUES ($1, 'L1 / D1', 'device', 'srt.error', 'error', now())`,
		unit); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	var path string
	if err := pool.QueryRow(ctx, `SELECT path FROM reporting.element WHERE id = $1`, unit).Scan(&path); err != nil || path != "L1 / D1" {
		t.Fatalf("reporting.element path = %q, %v; want L1 / D1", path, err)
	}

	if err := s.DeleteDevice(ctx, uuid.Nil, device); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	var deleted bool
	var reason string
	if err := pool.QueryRow(ctx,
		`SELECT element_path, element_deleted, cleared_reason FROM reporting.finding WHERE element_id = $1`, unit,
	).Scan(&path, &deleted, &reason); err != nil {
		t.Fatalf("reporting.finding: %v", err)
	}
	if path != "L1 / D1" || !deleted || reason != "deleted" {
		t.Fatalf("reporting.finding = %q, deleted %v, reason %q; want stored path, deleted, reason deleted", path, deleted, reason)
	}
}
