package supervisor

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/sdktest"
	"github.com/lukirs95/monika/sdk/vocab"
)

// invStore: ApplyDiscovery gelingt immer, ohne verschwundene Elemente.
type invStore struct{ inventory.Store }

func (invStore) ApplyDiscovery(context.Context, inventory.Discovery) ([]uuid.UUID, error) {
	return nil, nil
}

// evalStore: Schreiben gelingt immer.
type evalStore struct{ evaluation.Store }

func (evalStore) RaiseFinding(context.Context, evaluation.Finding) error              { return nil }
func (evalStore) EndFinding(context.Context, uuid.UUID, evaluation.ClearReason) error { return nil }
func (evalStore) SetFindingCause(context.Context, uuid.UUID, uuid.UUID) error         { return nil }
func (evalStore) SaveHealth(context.Context, []evaluation.ElementHealth) error        { return nil }

// TestCoreAndSupervisor verdrahtet die echte Schleife mit dem Supervisor und dem Fake-Treiber.
func TestCoreAndSupervisor(t *testing.T) {
	fake := sdktest.NewFake("fake")
	dev := fake.Device("10.0.0.1", sdk.Snapshot{
		Elements: []sdk.Element{{ExternalID: "sdi/1", Kind: vocab.ElementPort, Name: "SDI 1"}},
		Facts:    map[string]sdk.Facts{"sdi/1": {vocab.StateSignalPresent: true}},
	})
	dev.SetHeartbeat(time.Second)
	drivers := func(name string) (sdk.Driver, bool) { return fake.Driver(), name == "fake" }

	device, source, access := uuid.New(), uuid.New(), uuid.New()
	inv := inventory.New(invStore{}, drivers, discard())
	inv.Load(inventory.Snapshot{
		Sources: []inventory.Source{{ID: source, DeviceID: device, Driver: "fake", Address: "10.0.0.1", Enabled: true, AccessElementID: access}},
		Elements: []inventory.Element{
			{ID: device, Kind: inventory.KindDevice, DeviceID: device, Present: true, Monitored: true},
			{ID: access, Kind: inventory.KindAccess, ParentID: device, DeviceID: device, SourceID: source, Present: true, Monitored: true},
		},
	})
	ev := evaluation.New(evalStore{}, evaluation.RegistryFactTypes, func(string) bool { return true }, discard())

	sup := New(Config{Secrets: fakeSecrets{}, Scheduler: NewScheduler(4), Drivers: drivers, Log: discard()})
	sup.minBackoff = 200 * time.Millisecond // lang genug, damit Backoff in einer View erscheint
	c := core.New(core.Config{Inventory: inv, Evaluator: ev, Supervisor: sup, Log: discard()})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = sup.Run(ctx, c) })
	wg.Go(func() { _ = c.Run(ctx) })
	defer func() {
		cancel()
		wg.Wait()
	}()

	waitView := func(what string, ok func(*core.View) bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !ok(c.View()) {
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for %s; source %+v", what, c.View().Sources[source])
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitView("live", func(v *core.View) bool { return v.Sources[source].State == core.StateLive })
	port, _ := inv.ByExternalID(source, "sdi/1")
	waitView("facts", func(v *core.View) bool { return v.Facts[port][vocab.StateSignalPresent] == true })

	// Neustart über den Supervisor: eine neue Instanz, wieder Live, nie zwei Instanzen.
	first := sup.instance(source)
	sup.Start(core.SourceSpec{SourceID: source, DeviceID: device, Driver: "fake", Address: "10.0.0.1"})
	deadline := time.Now().Add(3 * time.Second)
	for inst := sup.instance(source); inst == nil || inst == first; inst = sup.instance(source) {
		if time.Now().After(deadline) {
			t.Fatal("no new instance after restart")
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitView("live after restart", func(v *core.View) bool { return v.Sources[source].State == core.StateLive })
	if dev.Running() != 1 {
		t.Errorf("running instances = %d", dev.Running())
	}

	// Panic in Run: Backoff, dann wieder Live.
	dev.PanicRun()
	waitView("backoff", func(v *core.View) bool {
		st := v.Sources[source]
		return st.State == core.StateBackoff && strings.Contains(st.Error, "panic in Run")
	})
	waitView("live after panic", func(v *core.View) bool { return v.Sources[source].State == core.StateLive })
}

// instance liefert die laufende Instanz einer Quelle (für Tests).
func (s *Supervisor) instance(id uuid.UUID) *instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}
