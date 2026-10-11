//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// newDeviceWithSource legt ein Gerät mit einer Quelle des angegebenen Treibers an.
func newDeviceWithSource(t *testing.T, q *db.Queries, driver string) (device, source uuid.UUID) {
	t.Helper()
	device, source = newID(), newID()
	if err := q.CreateDevice(t.Context(), db.CreateDeviceParams{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	newSource(t, q, device, source, driver)
	return device, source
}

func newSource(t *testing.T, q *db.Queries, device, source uuid.UUID, driver string) {
	t.Helper()
	n, err := q.CreateDeviceSource(t.Context(), db.CreateDeviceSourceParams{
		ID:       source,
		AccessID: newID(),
		DeviceID: device,
		Driver:   driver,
		Config:   json.RawMessage(`{}`),
		Enabled:  true,
	})
	if err != nil || n != 1 {
		t.Fatalf("CreateDeviceSource = %d, %v; want 1, nil", n, err)
	}
}

// driverElement liefert Upsert-Parameter für ein Element direkt unter dem Gerät.
func driverElement(device, source uuid.UUID, externalID string) db.UpsertDriverElementParams {
	return db.UpsertDriverElementParams{
		ID:              newID(),
		Kind:            "port",
		ParentID:        device,
		DeviceID:        device,
		SourceID:        source,
		ExternalID:      externalID,
		Attributes:      json.RawMessage(`{}`),
		Actions:         []string{},
		CriticalActions: []string{},
	}
}

func upsert(ctx context.Context, q *db.Queries, p db.UpsertDriverElementParams) (id uuid.UUID, err error) {
	q.UpsertDriverElement(ctx, []db.UpsertDriverElementParams{p}).QueryRow(func(_ int, got uuid.UUID, e error) {
		id, err = got, e
	})
	return id, err
}

func element(t *testing.T, q *db.Queries, id uuid.UUID) db.Element {
	t.Helper()
	elements, err := q.ListElements(t.Context())
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	i := slices.IndexFunc(elements, func(e db.Element) bool { return e.ID == id })
	if i < 0 {
		t.Fatalf("element %s not found", id)
	}
	return elements[i]
}

// count führt eine Abfrage mit einer Zahl als Ergebnis aus.
func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func TestCreateDevice(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q := db.New(server.NewDB(t))

	id := newID()
	if err := q.CreateDevice(ctx, db.CreateDeviceParams{ID: id, Label: new("Lokal 1")}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if e := element(t, q, id); e.Kind != "device" || e.DeviceID == nil || *e.DeviceID != id || e.FirstSeenAt != nil {
		t.Fatalf("device element = %+v, want kind device, device_id = id, never seen", e)
	}

}

func TestUpsertDriverElement(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q := db.New(server.NewDB(t))
	device, source := newDeviceWithSource(t, q, "videoxlink")

	first := driverElement(device, source, "sdi/1")
	id, err := upsert(ctx, q, first)
	if err != nil || id != first.ID {
		t.Fatalf("insert: id = %s, err = %v; want %s", id, err, first.ID)
	}

	t.Run("keeps id and user fields", func(t *testing.T) {
		if _, err := q.UpdateElementUserFields(ctx, db.UpdateElementUserFieldsParams{
			ID: id, Label: new("Kamera 1"), Monitored: true,
		}); err != nil {
			t.Fatalf("UpdateElementUserFields: %v", err)
		}
		again := driverElement(device, source, "sdi/1")
		again.Name = new("SDI In 1")
		got, err := upsert(ctx, q, again)
		if err != nil || got != id {
			t.Fatalf("update: id = %s, err = %v; want %s", got, err, id)
		}
		if e := element(t, q, id); *e.Label != "Kamera 1" || *e.Name != "SDI In 1" {
			t.Fatalf("element = %+v, want label kept and name updated", e)
		}
	})

	t.Run("same external_id in other source is a separate element", func(t *testing.T) {
		other := newID()
		newSource(t, q, device, other, "ipmi")
		e := driverElement(device, other, "sdi/1")
		got, err := upsert(ctx, q, e)
		if err != nil || got != e.ID || got == id {
			t.Fatalf("upsert = %s, %v; want new element %s", got, err, e.ID)
		}
	})
}

func TestFinishDiscovery(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q := db.New(server.NewDB(t))
	device, source := newDeviceWithSource(t, q, "videoxlink")

	var ids []uuid.UUID
	for _, ext := range []string{"sdi/1", "sdi/2", "sdi/3"} {
		id, err := upsert(ctx, q, driverElement(device, source, ext))
		if err != nil {
			t.Fatalf("upsert %s: %v", ext, err)
		}
		ids = append(ids, id)
	}
	before := element(t, q, ids[0])

	missing, err := q.FinishDiscovery(ctx, db.FinishDiscoveryParams{SourceID: source, SeenIds: ids[:2]})
	if err != nil || !slices.Equal(missing, ids[2:]) {
		t.Fatalf("FinishDiscovery = %v, %v; want %v", missing, err, ids[2:])
	}
	// Auch ohne Änderung am Element ist es frisch gesehen.
	if e := element(t, q, ids[0]); !e.LastSeenAt.After(*before.LastSeenAt) {
		t.Fatalf("last_seen_at = %v, want after %v", e.LastSeenAt, before.LastSeenAt)
	}
	lastSeen := element(t, q, ids[2]).LastSeenAt

	// Meldet die Quelle nichts mehr, verschwinden alle übrigen Elemente.
	missing, err = q.FinishDiscovery(ctx, db.FinishDiscoveryParams{SourceID: source, SeenIds: nil})
	slices.SortFunc(missing, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if err != nil || !slices.Equal(missing, ids[:2]) {
		t.Fatalf("FinishDiscovery(nil) = %v, %v; want %v", missing, err, ids[:2])
	}
	if e := element(t, q, ids[2]); e.Present || !e.LastSeenAt.Equal(*lastSeen) {
		t.Fatalf("element = %+v, want missing with last_seen_at kept at %v", e, lastSeen)
	}
}
