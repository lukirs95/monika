package evaluation

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
)

func (b *bench) alarms(replace bool, alarms map[uuid.UUID][]sdk.Alarm) []FindingChange {
	b.t.Helper()
	changes, err := b.ev.Alarms(b.t.Context(), b.now, b.source, alarms, replace, b.state)
	if err != nil {
		b.t.Fatalf("Alarms: %v", err)
	}
	return changes
}

func TestAlarms(t *testing.T) {
	b := newBench(t, tempRule())
	los := sdk.Alarm{Code: "los", Severity: sdk.SeverityError, Message: "loss of signal"}
	odd := sdk.Alarm{Code: "odd", Severity: "fatal"}

	if got := summary(b.alarms(true, map[uuid.UUID][]sdk.Alarm{b.port: {los, los, odd}})); got != "+ +" {
		t.Fatalf("raise: %q", got)
	}
	for _, f := range b.store.raised {
		want := map[string]Severity{"los": SeverityError, "odd": SeverityWarning}[f.Code]
		if f.Origin != OriginDevice || f.Severity != want || f.RuleID != uuid.Nil {
			t.Errorf("finding = %+v", f)
		}
	}
	if got := summary(b.alarms(false, map[uuid.UUID][]sdk.Alarm{b.access: nil})); got != "" {
		t.Errorf("update of another element: %q", got)
	}
	if got := summary(b.alarms(false, map[uuid.UUID][]sdk.Alarm{b.port: {los}})); got != "-resolved" {
		t.Errorf("update drops odd: %q", got)
	}

	b.state.notLive = map[uuid.UUID]bool{b.source: true}
	if got := summary(b.alarms(true, nil)); got != "" {
		t.Errorf("not live: %q, want frozen", got)
	}
	b.state.notLive = nil
	if got := summary(b.evaluate(b.port)); got != "-resolved" {
		t.Errorf("live again, snapshot without alarms: %q", got)
	}

	// Ein verschwundenes Element: Seine Gerätebefunde werden zurückgezogen.
	b.alarms(true, map[uuid.UUID][]sdk.Alarm{b.port: {los}})
	b.change(b.port, func(e *inventory.Element) { e.Present = false })
	if got := summary(b.evaluate(b.port)); got != "-element_missing" {
		t.Errorf("missing element: %q", got)
	}
}

func TestSourceUnreachable(t *testing.T) {
	b := newBench(t, tempRule())
	b.state.notLive = map[uuid.UUID]bool{b.source: true}
	b.state.unreachable = map[uuid.UUID]string{b.source: "no_heartbeat"}

	if got := summary(b.tick(time.Second)); got != "+" {
		t.Fatalf("unreachable: %q", got)
	}
	f := b.store.raised[0]
	if f.ElementID != b.access || f.Code != CodeSourceUnreachable || f.Origin != OriginSystem || string(f.Details) != `{"reason":"no_heartbeat"}` {
		t.Errorf("finding = %+v, details %s", f, f.Details)
	}
	if got := summary(b.tick(time.Second)); got != "" {
		t.Errorf("still unreachable: %q", got)
	}
	b.state.unreachable = nil
	if got := summary(b.tick(time.Second)); got != "" {
		t.Errorf("reachable, not yet live: %q, want kept", got)
	}
	b.state.notLive = nil
	if got := summary(b.tick(time.Second)); got != "-resolved" {
		t.Errorf("live again: %q", got)
	}

	b.state.unreachable = map[uuid.UUID]string{b.source: "failed"}
	b.tick(time.Second)
	b.setSource(func(s *inventory.Source) { s.Enabled = false })
	if got := summary(b.tick(time.Second)); got != "-source_disabled" {
		t.Errorf("source disabled: %q", got)
	}
}

func TestDiscovery(t *testing.T) {
	b := newBench(t, tempRule())
	discovery := func(err error) string {
		changes, e := b.ev.Discovery(t.Context(), b.now, b.source, err, b.state)
		if e != nil {
			t.Fatalf("Discovery: %v", e)
		}
		return summary(changes)
	}
	if got := discovery(errors.New("global ref conflict")); got != "+" {
		t.Fatalf("failed: %q", got)
	}
	if f := b.store.raised[0]; f.ElementID != b.access || f.Code != CodeDiscoveryFailed || string(f.Details) != `{"error":"global ref conflict"}` {
		t.Errorf("finding = %+v, details %s", f, f.Details)
	}
	if got := discovery(errors.New("again")); got != "" {
		t.Errorf("failed again: %q", got)
	}
	if got := discovery(nil); got != "-resolved" {
		t.Errorf("success: %q", got)
	}
}

func TestRuleCommands(t *testing.T) {
	b := newBench(t, tempRule()) // global: temp lte 80
	ctx, actor := t.Context(), uuid.New()
	b.set("temp", 85.0)
	global := b.store.raised[0].ID

	// Eine spezifischere Regel am Port überschreibt die globale; sie ist erfüllt.
	local := Rule{ID: uuid.New(), Name: "Port", Enabled: true, Severity: SeverityError, ScopeElementID: b.port, Fact: "temp", Op: OpLte, Value: raw(`90`)}
	changes, err := b.ev.CreateRule(ctx, b.now, actor, local, b.state)
	if err != nil || len(changes) != 1 || changes[0].Ended != global || changes[0].Reason != ReasonRuleChanged {
		t.Fatalf("CreateRule = %+v, %v; want global finding withdrawn as rule_changed", changes, err)
	}

	// Strenger: verletzt.
	local.Value = raw(`84`)
	if changes, err = b.ev.UpdateRule(ctx, b.now, actor, local, b.state); err != nil || summary(changes) != "+" {
		t.Fatalf("UpdateRule = %q, %v", summary(changes), err)
	}
	localFinding := b.store.raised[1].ID

	// Deaktiviert: Der Store hat zurückgezogen; die globale Regel gilt wieder.
	local.Enabled = false
	changes, err = b.ev.UpdateRule(ctx, b.now, actor, local, b.state)
	if err != nil || summary(changes) != "-rule_disabled +" || changes[0].Ended != localFinding {
		t.Fatalf("UpdateRule disable = %q, %v", summary(changes), err)
	}
	if _, written := b.store.ended[localFinding]; written {
		t.Error("rule_disabled written again")
	}
	if r, ok := b.ev.Rules().Rule(local.ID); !ok || r.Enabled {
		t.Error("disabled rule must stay visible")
	}

	// Gelöscht: Der Befund der globalen Regel endet im Speicher.
	globalAgain := b.store.raised[2].ID
	changes, err = b.ev.DeleteRule(ctx, b.now, actor, b.rule.ID, b.state)
	if err != nil || len(changes) != 1 || changes[0].Ended != globalAgain || changes[0].Reason != ReasonRuleDeleted {
		t.Fatalf("DeleteRule = %+v, %v", changes, err)
	}
	if _, ok := b.ev.Rules().Rule(b.rule.ID); ok {
		t.Error("deleted rule still there")
	}
}

func TestRuleCommandErrors(t *testing.T) {
	b := newBench(t, tempRule())
	valid := func() Rule {
		return Rule{ID: uuid.New(), Name: "R", Enabled: true, Severity: SeverityError, Fact: "temp", Op: OpLte, Value: raw(`80`)}
	}
	tests := []struct {
		name   string
		change func(*Rule)
	}{
		{"missing ID", func(r *Rule) { r.ID = uuid.Nil }},
		{"unknown scope", func(r *Rule) { r.ScopeElementID = uuid.New() }},
		{"unknown driver", func(r *Rule) { r.MatchDriver = "acme" }},
		{"unknown fact", func(r *Rule) { r.Fact = "nope" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid()
			tt.change(&r)
			if _, err := b.ev.CreateRule(t.Context(), b.now, uuid.New(), r, b.state); !errors.Is(err, ErrInvalidRule) {
				t.Fatalf("CreateRule = %v, want ErrInvalidRule", err)
			}
			if _, err := b.ev.UpdateRule(t.Context(), b.now, uuid.New(), r, b.state); !errors.Is(err, ErrInvalidRule) {
				t.Fatalf("UpdateRule = %v, want ErrInvalidRule", err)
			}
			if len(b.store.calls) != 0 {
				t.Errorf("store called: %v", b.store.calls)
			}
		})
	}
}

func TestAckFinding(t *testing.T) {
	b := newBench(t, tempRule())
	ctx := t.Context()
	if _, err := b.ev.AckFinding(ctx, b.now, uuid.New(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AckFinding unknown = %v", err)
	}
	b.set("temp", 81.0)
	id := b.store.raised[0].ID
	first := uuid.New()
	f, err := b.ev.AckFinding(ctx, b.now, first, id)
	if err != nil || f.AckedByID != first || !f.AckedAt.Equal(b.now) {
		t.Fatalf("AckFinding = %+v, %v", f, err)
	}
	f, _ = b.ev.AckFinding(ctx, b.now.Add(time.Minute), uuid.New(), id)
	if f.AckedByID != first || !b.ev.Findings()[id].AckedAt.Equal(b.now) {
		t.Errorf("second ack changed the first: %+v", f)
	}
}
