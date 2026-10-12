package core

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
)

func (r *rig) do(cmd Command) error {
	r.t.Helper()
	return r.c.Do(r.t.Context(), cmd)
}

func TestStartSourcesOnRun(t *testing.T) {
	r := newRig(t)
	r.waitView("source starting", func(v *View) bool { return v.Sources[r.source].State == StateStarting })
	if got := r.sup.Calls(); !slices.Equal(got, []string{"start videoxlink"}) {
		t.Errorf("supervisor calls = %v", got)
	}
}

func TestCommandsInventory(t *testing.T) {
	r := newRig(t)
	device, source := uuid.New(), uuid.New()
	if err := r.do(CreateDevice{Device: inventory.Element{ID: device, Label: "X9"}}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	// Do kehrt erst zurück, wenn die View das Ergebnis zeigt.
	if _, ok := r.c.View().Graph.Element(device); !ok {
		t.Fatal("device not in the view after Do")
	}
	if err := r.do(CreateSource{Source: inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink", Enabled: true}}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if v := r.c.View(); v.Sources[source].State != StateStarting {
		t.Errorf("new source = %+v", v.Sources[source])
	}
	if err := r.do(UpdateSource{Source: inventory.Source{ID: source, Enabled: false}}); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}
	if v := r.c.View(); v.Sources[source].State != StateDisabled {
		t.Errorf("disabled source = %+v", v.Sources[source])
	}
	if got := r.sup.Calls(); !slices.Equal(got, []string{"start videoxlink", "start videoxlink", "stop"}) {
		t.Errorf("supervisor calls = %v", got)
	}

	// Fachliche Fehler kommen bei der API an und berühren den Zustand der Datenbank nicht.
	err := r.do(AddGroupMember{Link: inventory.Link{ID: uuid.New(), FromID: device, ToID: r.access}})
	if !errors.Is(err, inventory.ErrNotAGroup) || !r.c.View().DB.OK {
		t.Errorf("AddGroupMember = %v, db ok %v", err, r.c.View().DB.OK)
	}
	// Auch Konflikte, die erst der Store erkennt.
	r.store.setErr(inventory.ErrSourceExists)
	err = r.do(CreateSource{Source: inventory.Source{ID: uuid.New(), DeviceID: device, Driver: "videoxlink", Enabled: true}})
	if !errors.Is(err, inventory.ErrSourceExists) || !r.c.View().DB.OK {
		t.Errorf("CreateSource = %v, db ok %v", err, r.c.View().DB.OK)
	}
}

func TestCommandsDeleteSourceWithdrawsFindings(t *testing.T) {
	r := newRig(t)
	rep := r.c.Reporter(r.source)
	port := r.goLive(rep, 85)
	r.collect("finding raised", func(e Event) bool { _, ok := e.(FindingRaised); return ok })

	if err := r.do(DeleteSource{ID: r.source}); err != nil {
		t.Fatalf("DeleteSource: %v", err)
	}
	events := r.collect("finding ended", func(e Event) bool { _, ok := e.(FindingEnded); return ok })
	if fe := events[len(events)-1].(FindingEnded); fe.Reason != evaluation.ReasonDeleted || fe.Finding.ElementID != port {
		t.Errorf("FindingEnded = %+v", fe)
	}
	v := r.c.View()
	if _, ok := v.Graph.Element(port); ok || len(v.Facts[port]) != 0 || len(v.Findings) != 0 {
		t.Errorf("port %v, facts %v, findings %d", ok, v.Facts[port], len(v.Findings))
	}
	if _, ok := v.Sources[r.source]; ok {
		t.Error("deleted source still in the view")
	}
	// Meldungen der gelöschten Quelle fallen weg.
	rep.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/1": {"temp": 99.0}}})
}

func TestCommandsRulesAndAck(t *testing.T) {
	r := newRig(t)
	port := r.goLive(r.c.Reporter(r.source), 70)

	err := r.do(CreateRule{Rule: evaluation.Rule{ID: uuid.New(), Name: "x", Severity: evaluation.SeverityError, Fact: "nope", Op: evaluation.OpEq, Value: []byte(`1`)}})
	if !errors.Is(err, evaluation.ErrInvalidRule) {
		t.Fatalf("invalid rule = %v", err)
	}
	strict := evaluation.Rule{ID: uuid.New(), Name: "Port", Enabled: true, Severity: evaluation.SeverityError,
		ScopeElementID: port, Fact: "temp", Op: evaluation.OpLte, Value: []byte(`60`)}
	if err := r.do(CreateRule{Rule: strict}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	v := r.c.View()
	if _, ok := v.Rules.Rule(strict.ID); !ok || len(v.Findings) != 1 {
		t.Fatalf("rule in view %v, findings %d", ok, len(v.Findings))
	}
	var id uuid.UUID
	for id = range v.Findings {
	}
	actor := uuid.New()
	if err := r.do(AckFinding{Actor: actor, ID: id}); err != nil {
		t.Fatalf("AckFinding: %v", err)
	}
	if f := r.c.View().Findings[id]; f.AckedByID != actor || f.AckedAt.IsZero() {
		t.Errorf("acked finding = %+v", f)
	}
	r.collect("finding acked", func(e Event) bool { _, ok := e.(FindingAcked); return ok })
}

func TestCommandsReplaceSecret(t *testing.T) {
	r := newRig(t)
	secret, device, source := uuid.New(), uuid.New(), uuid.New()
	if err := r.do(CreateDevice{Device: inventory.Element{ID: device}}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := r.do(CreateSource{Source: inventory.Source{ID: source, DeviceID: device, Driver: "videoxlink", SecretID: secret, Enabled: true}}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if err := r.do(ReplaceSecret{ID: secret, Value: []byte(`{"password":"x"}`)}); err != nil {
		t.Fatalf("ReplaceSecret: %v", err)
	}
	if r.secrets.replaced.Load() != 1 {
		t.Error("secret not replaced")
	}
	if got := r.sup.Calls(); !slices.Equal(got, []string{"start videoxlink", "start videoxlink", "stop", "start videoxlink"}) {
		t.Errorf("supervisor calls = %v, want the source using the secret restarted", got)
	}
}

func TestDBWatcherAndShutdown(t *testing.T) {
	r := newRig(t)
	down := errors.New("connection refused")
	r.pinger.err.Store(&down)
	r.waitView("db down", func(v *View) bool { return !v.DB.OK && v.DB.Error == "connection refused" })
	r.pinger.err.Store(nil)
	r.waitView("db up", func(v *View) bool { return v.DB.OK })

	r.stop()
	if r.saves.Load() == 0 {
		t.Error("no SaveHealth on shutdown")
	}
	if err := r.c.Do(t.Context(), CreateDevice{Device: inventory.Element{ID: uuid.New()}}); !errors.Is(err, ErrStopped) {
		t.Errorf("Do after stop = %v", err)
	}
}
