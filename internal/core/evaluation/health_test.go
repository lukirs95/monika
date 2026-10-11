package evaluation

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
)

// healthOf liefert Health und Rollup eines Elements nach Health(s, nil).
func (b *bench) health() map[uuid.UUID]ElementHealth {
	b.t.Helper()
	b.ev.Health(b.state, nil)
	return b.ev.Healths()
}

func TestHealth(t *testing.T) {
	b := newBench(t, tempRule()) // global: temp lte 80; nur der Port hat temp
	h := b.health()
	want := map[uuid.UUID]Health{
		b.port: HealthOK, b.access: HealthNone, b.missing: HealthNone, b.unmonitored: HealthNone,
		b.device: HealthOK, b.g1: HealthOK, b.g2: HealthOK,
	}
	for id, w := range want {
		if h[id].Health != w {
			t.Errorf("health of %s = %q, want %q", b.name(id), h[id].Health, w)
		}
	}
	if r := h[b.device].Rollup; r == nil || *r != (Rollup{OK: 1, Unchecked: 1, Missing: 1, Unmonitored: 1}) {
		t.Errorf("device rollup = %+v", r)
	}
	if r := h[b.g2].Rollup; r == nil || *r != (Rollup{OK: 1}) {
		t.Errorf("nested group rollup = %+v", r)
	}
	if h[b.port].Rollup != nil {
		t.Error("element with rollup")
	}

	// Ein Zugang mit Lebenszeichen ist geprüft.
	b.state.heartbeat = 5 * time.Second
	if got := b.ev.Health(b.state, []uuid.UUID{b.access}); len(got) != 2 || b.ev.Healths()[b.access].Health != HealthOK {
		t.Errorf("access with heartbeat: changed %+v", got)
	}
	if got := b.ev.Health(b.state, nil); len(got) != 0 {
		t.Errorf("unchanged: %+v", got)
	}

	// Der Port verletzt die Regel: Port, Gerät und beide Gruppen werden warning.
	b.set("temp", 85.0)
	changed := b.ev.Health(b.state, nil)
	if len(changed) != 4 {
		t.Errorf("changed = %+v, want port, device, g1, g2", changed)
	}
	for _, id := range []uuid.UUID{b.port, b.device, b.g1, b.g2} {
		if got := b.ev.Healths()[id].Health; got != HealthWarning {
			t.Errorf("health of %s = %q, want warning", b.name(id), got)
		}
	}
}

func TestHealthNotLive(t *testing.T) {
	b := newBench(t, tempRule())
	b.state.heartbeat = 5 * time.Second
	b.set("temp", 85.0)
	b.state.notLive = map[uuid.UUID]bool{b.source: true}
	h := b.health()
	if h[b.port].Health != HealthUnknown || h[b.access].Health != HealthUnknown || h[b.device].Health != HealthUnknown {
		t.Errorf("not live: port %q, access %q, device %q; want unknown", h[b.port].Health, h[b.access].Health, h[b.device].Health)
	}
	// Der Zugang zeigt source.unreachable.
	b.state.unreachable = map[uuid.UUID]string{b.source: "disconnected"}
	b.tick(time.Second)
	h = b.health()
	if h[b.access].Health != HealthError || h[b.port].Health != HealthUnknown || h[b.device].Health != HealthError {
		t.Errorf("unreachable: access %q, port %q, device %q", h[b.access].Health, h[b.port].Health, h[b.device].Health)
	}
}

func TestHealthRollupSpecialCases(t *testing.T) {
	t.Run("missing element with finding counts", func(t *testing.T) {
		b := newBench(t, Rule{ScopeElementID: uuid.Nil, Fact: "present", Op: OpEq, Value: raw(`true`), Severity: SeverityError})
		b.evaluate(b.missing)
		h := b.health()
		if h[b.missing].Health != HealthNone || h[b.device].Health != HealthError || h[b.device].Rollup.Error != 1 || h[b.device].Rollup.Missing != 0 {
			t.Errorf("missing: %q, device %q %+v", h[b.missing].Health, h[b.device].Health, h[b.device].Rollup)
		}
	})
	t.Run("device out of service", func(t *testing.T) {
		b := newBench(t, tempRule())
		b.change(b.device, func(e *inventory.Element) { e.Monitored = false })
		h := b.health()
		if h[b.device].Health != HealthNone || *h[b.device].Rollup != (Rollup{Unmonitored: 4}) {
			t.Errorf("device = %q %+v", h[b.device].Health, h[b.device].Rollup)
		}
		if h[b.g1].Health != HealthNone || *h[b.g1].Rollup != (Rollup{Unmonitored: 1}) {
			t.Errorf("group with the port = %q %+v", h[b.g1].Health, h[b.g1].Rollup)
		}
	})
	t.Run("empty group", func(t *testing.T) {
		b := newBench(t, tempRule())
		b.snap.Links = nil
		b.rebuild()
		h := b.health()
		if h[b.g1].Health != HealthNone || *h[b.g1].Rollup != (Rollup{}) {
			t.Errorf("empty group = %q %+v", h[b.g1].Health, h[b.g1].Rollup)
		}
	})
}

func TestSaveHealth(t *testing.T) {
	b := newBench(t, tempRule())
	b.health()
	b.store.saveErr = errors.New("db down")
	if _, err := b.ev.Tick(t.Context(), b.now, b.state); !errors.Is(err, b.store.saveErr) {
		t.Fatalf("Tick = %v, want save error", err)
	}
	b.store.saveErr = nil
	b.tick(time.Second)
	if len(b.store.saved) != 1 || len(b.store.saved[0]) != 7 {
		t.Fatalf("saved = %d batches, want one with all 7 elements", len(b.store.saved))
	}
	b.tick(time.Second)
	if len(b.store.saved) != 1 {
		t.Error("saved again without change")
	}

	// Gelöschte Elemente werden nicht mehr gespeichert.
	b.set("temp", 85.0)
	b.health()
	b.remove(b.port)
	b.health()
	b.tick(time.Second)
	for _, h := range b.store.saved[len(b.store.saved)-1] {
		if h.ElementID == b.port {
			t.Error("deleted element saved")
		}
	}
}

func (b *bench) name(id uuid.UUID) string {
	return map[uuid.UUID]string{
		b.device: "device", b.access: "access", b.port: "port", b.missing: "missing",
		b.unmonitored: "unmonitored", b.g1: "g1", b.g2: "g2",
	}[id]
}
