package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
)

// fakeStore ist die Attrappe des Stores; sie merkt sich Befunde und kann Schreibvorgänge
// scheitern lassen.
type fakeStore struct {
	raised   []Finding
	ended    map[uuid.UUID]ClearReason
	raiseErr error
	endErr   error
	calls    []string // Regeln und Quittieren
}

var _ Store = (*fakeStore)(nil)

func (f *fakeStore) RaiseFinding(_ context.Context, fd Finding) error {
	if f.raiseErr != nil {
		return f.raiseErr
	}
	f.raised = append(f.raised, fd)
	return nil
}

func (f *fakeStore) EndFinding(_ context.Context, id uuid.UUID, reason ClearReason) error {
	if f.endErr != nil {
		return f.endErr
	}
	if f.ended == nil {
		f.ended = map[uuid.UUID]ClearReason{}
	}
	f.ended[id] = reason
	return nil
}

func (f *fakeStore) ListRules(context.Context) ([]Rule, error) { return nil, nil }
func (f *fakeStore) CreateRule(context.Context, uuid.UUID, Rule) error {
	f.calls = append(f.calls, "create")
	return nil
}
func (f *fakeStore) UpdateRule(context.Context, uuid.UUID, Rule) error {
	f.calls = append(f.calls, "update")
	return nil
}
func (f *fakeStore) DeleteRule(context.Context, uuid.UUID, uuid.UUID) error {
	f.calls = append(f.calls, "delete")
	return nil
}
func (f *fakeStore) ListActiveFindings(context.Context) ([]Finding, error)       { return nil, nil }
func (f *fakeStore) SetFindingCause(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeStore) AckFinding(context.Context, uuid.UUID, uuid.UUID) error {
	f.calls = append(f.calls, "ack")
	return nil
}
func (f *fakeStore) LoadHealth(context.Context) ([]ElementHealth, error) { return nil, nil }
func (f *fakeStore) SaveHealth(context.Context, []ElementHealth) error   { return nil }

// bench ist ein Evaluator über der Testwelt mit einer Regel am Port.
type bench struct {
	*world
	ev    *Evaluator
	store *fakeStore
	rule  Rule
	now   time.Time
	t     *testing.T
}

func newBench(t *testing.T, r Rule) *bench {
	t.Helper()
	w := newWorld(t)
	store := &fakeStore{}
	ev := New(store, testTypes, func(name string) bool { return name == "videoxlink" }, slog.New(slog.DiscardHandler))
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	r.Name, r.Enabled = "Temperatur", true
	if r.Severity == "" {
		r.Severity = SeverityWarning
	}
	ev.Load([]Rule{r}, nil)
	return &bench{world: w, ev: ev, store: store, rule: r, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), t: t}
}

func tempRule(change ...func(*Rule)) Rule {
	r := Rule{ScopeElementID: uuid.Nil, Fact: "temp", Op: OpLte, Value: raw(`80`)}
	for _, c := range change {
		c(&r)
	}
	return r
}

// set setzt einen Fakt am Port und bewertet ihn.
func (b *bench) set(key string, v any) []FindingChange {
	b.t.Helper()
	b.state.facts[b.port][key] = v
	return b.evaluate(b.port)
}

func (b *bench) evaluate(ids ...uuid.UUID) []FindingChange {
	b.t.Helper()
	changes, err := b.ev.Evaluate(b.t.Context(), b.now, ids, b.state)
	if err != nil {
		b.t.Fatalf("Evaluate: %v", err)
	}
	return changes
}

func (b *bench) tick(d time.Duration) []FindingChange {
	b.t.Helper()
	b.now = b.now.Add(d)
	changes, err := b.ev.Tick(b.t.Context(), b.now, b.state)
	if err != nil {
		b.t.Fatalf("Tick: %v", err)
	}
	return changes
}

// summary beschreibt Änderungen kurz: "+" für entstanden, "-grund" für beendet.
func summary(changes []FindingChange) string {
	var out []string
	for _, c := range changes {
		if c.Raised != nil {
			out = append(out, "+")
		} else {
			out = append(out, "-"+string(c.Reason))
		}
	}
	return strings.Join(out, " ")
}

func TestEvaluateRaiseAndResolve(t *testing.T) {
	b := newBench(t, tempRule())
	if got := summary(b.set("temp", 81.0)); got != "+" {
		t.Fatalf("raise: %q", got)
	}
	f := b.store.raised[0]
	if f.ElementID != b.port || f.RuleID != b.rule.ID || f.Code != CodeRuleViolated || f.Severity != SeverityWarning ||
		f.ElementPath != "X8 / e" || f.Message != "temp lte 80" || !f.RaisedAt.Equal(b.now) {
		t.Errorf("finding = %+v", f)
	}
	var details struct {
		Rule  ruleSnapshot `json:"rule"`
		Value float64      `json:"value"`
	}
	if err := json.Unmarshal(f.Details, &details); err != nil || details.Rule.Name != "Temperatur" || details.Value != 81 {
		t.Errorf("details = %s, %v", f.Details, err)
	}
	if _, ok := b.ev.Findings()[f.ID]; !ok {
		t.Error("finding not active")
	}

	if got := summary(b.set("temp", 82.0)); got != "" {
		t.Errorf("still violated: %q", got)
	}
	if got := summary(b.set("temp", 79.0)); got != "-resolved" {
		t.Errorf("resolve: %q", got)
	}
	if b.store.ended[f.ID] != ReasonResolved || len(b.ev.Findings()) != 0 {
		t.Errorf("ended = %v, active = %v", b.store.ended, b.ev.Findings())
	}
}

func TestEvaluateHoldTimes(t *testing.T) {
	b := newBench(t, tempRule(func(r *Rule) { r.HoldSeconds, r.ClearHoldSeconds = 10, 5 }))
	steps := []struct {
		name string
		do   func() []FindingChange
		want string
	}{
		{"violated", func() []FindingChange { return b.set("temp", 81.0) }, ""},
		{"hold not over", func() []FindingChange { return b.tick(9 * time.Second) }, ""},
		{"satisfied before hold", func() []FindingChange { return b.set("temp", 70.0) }, ""},
		{"no raise after pending dropped", func() []FindingChange { return b.tick(5 * time.Second) }, ""},
		{"violated again", func() []FindingChange { return b.set("temp", 81.0) }, ""},
		{"hold over", func() []FindingChange { return b.tick(10 * time.Second) }, "+"},
		{"satisfied: clearing", func() []FindingChange { return b.set("temp", 70.0) }, ""},
		{"violated during clearing", func() []FindingChange { return b.set("temp", 81.0) }, ""},
		{"clear hold would be over", func() []FindingChange { return b.tick(10 * time.Second) }, ""},
		{"satisfied again", func() []FindingChange { return b.set("temp", 70.0) }, ""},
		{"clear hold not over", func() []FindingChange { return b.tick(4 * time.Second) }, ""},
		{"clear hold over", func() []FindingChange { return b.tick(time.Second) }, "-resolved"},
	}
	for _, s := range steps {
		if got := summary(s.do()); got != s.want {
			t.Fatalf("%s: changes %q, want %q", s.name, got, s.want)
		}
	}
}

func TestEvaluateHysteresis(t *testing.T) {
	b := newBench(t, tempRule(func(r *Rule) { r.ClearValue = raw(`75`) }))
	for _, s := range []struct {
		temp float64
		want string
	}{{81, "+"}, {78, ""}, {75.5, ""}, {75, "-resolved"}, {78, ""}} {
		if got := summary(b.set("temp", s.temp)); got != s.want {
			t.Fatalf("temp %v: changes %q, want %q", s.temp, got, s.want)
		}
	}
}

func TestEvaluateNotLive(t *testing.T) {
	b := newBench(t, tempRule())
	b.set("temp", 81.0)
	b.state.notLive = map[uuid.UUID]bool{b.source: true}
	if got := summary(b.set("temp", 70.0)); got != "" {
		t.Errorf("not live: changes %q, want none", got)
	}
	if got := summary(b.set("temp", 90.0)); got != "" || len(b.ev.Findings()) != 1 {
		t.Errorf("not live: changes %q, want finding kept", got)
	}
	b.state.notLive = nil
	if got := summary(b.set("temp", 70.0)); got != "-resolved" {
		t.Errorf("live again: changes %q", got)
	}
}

func TestEvaluateWithdraw(t *testing.T) {
	tests := []struct {
		name    string
		rule    func(*Rule)
		change  func(b *bench)
		want    ClearReason
		written bool
	}{
		{"element deleted", nil, func(b *bench) { b.remove(b.port) }, ReasonDeleted, false},
		{"source disabled", nil, func(b *bench) { b.setSource(func(s *inventory.Source) { s.Enabled = false }) }, ReasonSourceDisabled, true},
		{"unmonitored via parent", nil, func(b *bench) { b.change(b.access, func(e *inventory.Element) { e.Monitored = false }) }, ReasonUnmonitored, true},
		{"element missing", nil, func(b *bench) { b.change(b.port, func(e *inventory.Element) { e.Present = false }) }, ReasonElementMissing, true},
		{"fact missing", nil, func(b *bench) { delete(b.state.facts[b.port], "temp") }, ReasonFactMissing, true},
		{"when fact missing", func(r *Rule) { r.When = &Condition{Fact: "running", Op: OpEq, Value: raw(`true`)} },
			func(b *bench) { delete(b.state.facts[b.port], "running") }, ReasonFactMissing, true},
		{"filter no longer matches", func(r *Rule) { r.MatchTransport = "sdi" },
			func(b *bench) { b.change(b.port, func(e *inventory.Element) { e.Transport = "hdmi" }) }, ReasonRuleChanged, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t, tempRule(func(r *Rule) {
				if tt.rule != nil {
					tt.rule(r)
				}
			}))
			b.set("temp", 81.0)
			id := b.store.raised[0].ID
			tt.change(b)
			changes := b.evaluate(b.port)
			if len(changes) != 1 || changes[0].Ended != id || changes[0].Reason != tt.want {
				t.Fatalf("changes = %+v, want %s ended with %s", changes, id, tt.want)
			}
			if _, written := b.store.ended[id]; written != tt.written {
				t.Errorf("written = %v, want %v", written, tt.written)
			}
			if len(b.ev.Findings()) != 0 {
				t.Error("finding still active")
			}
		})
	}
}

func TestEvaluatePrunesRulesOfDeletedScope(t *testing.T) {
	b := newBench(t, tempRule())
	// Regel an der Gruppe g1, die den Port enthält.
	r := b.rule
	r.ScopeElementID = b.g1
	b.ev.Load([]Rule{r}, nil)
	b.set("temp", 81.0)
	id := b.store.raised[0].ID

	b.remove(b.g1)
	changes := b.evaluate(b.g1)
	if len(changes) != 1 || changes[0].Ended != id || changes[0].Reason != ReasonRuleDeleted {
		t.Fatalf("changes = %+v, want rule_deleted", changes)
	}
	if _, ok := b.ev.Rules().Rule(r.ID); ok || len(b.store.ended) != 0 {
		t.Errorf("rule still there or end written: %v", b.store.ended)
	}
}

func TestWriteFailures(t *testing.T) {
	down := errors.New("db down")

	t.Run("raise retried with same ID", func(t *testing.T) {
		b := newBench(t, tempRule())
		b.store.raiseErr = down
		b.state.facts[b.port]["temp"] = 81.0
		changes, err := b.ev.Evaluate(t.Context(), b.now, []uuid.UUID{b.port}, b.state)
		if !errors.Is(err, down) || len(changes) != 0 || len(b.ev.Findings()) != 0 {
			t.Fatalf("Evaluate = %+v, %v; want error, nothing visible", changes, err)
		}
		b.store.raiseErr = nil
		if got := summary(b.tick(time.Second)); got != "+" || len(b.store.raised) != 1 {
			t.Fatalf("retry: %q, raised %d", got, len(b.store.raised))
		}
	})

	t.Run("pending raise dropped when satisfied", func(t *testing.T) {
		b := newBench(t, tempRule())
		b.store.raiseErr = down
		b.state.facts[b.port]["temp"] = 81.0
		_, _ = b.ev.Evaluate(t.Context(), b.now, []uuid.UUID{b.port}, b.state)
		b.store.raiseErr = nil
		b.set("temp", 70.0)
		if got := summary(b.tick(time.Second)); got != "" || len(b.store.raised) != 0 {
			t.Fatalf("tick: %q, raised %d; want nothing", got, len(b.store.raised))
		}
	})

	t.Run("end retried", func(t *testing.T) {
		b := newBench(t, tempRule())
		b.set("temp", 81.0)
		b.store.endErr = down
		b.state.facts[b.port]["temp"] = 70.0
		if _, err := b.ev.Evaluate(t.Context(), b.now, []uuid.UUID{b.port}, b.state); !errors.Is(err, down) || len(b.ev.Findings()) != 1 {
			t.Fatalf("Evaluate = %v; want error and finding still active", err)
		}
		b.store.endErr = nil
		if got := summary(b.tick(time.Second)); got != "-resolved" {
			t.Fatalf("retry: %q", got)
		}
	})

	t.Run("end already done counts as success", func(t *testing.T) {
		b := newBench(t, tempRule())
		b.set("temp", 81.0)
		b.store.endErr = ErrNotFound
		if got := summary(b.set("temp", 70.0)); got != "-resolved" {
			t.Fatalf("changes %q", got)
		}
	})
}

func TestLoadActiveFindings(t *testing.T) {
	b := newBench(t, tempRule())
	f := Finding{ID: uuid.New(), ElementID: b.port, Origin: OriginRule, RuleID: b.rule.ID, Code: CodeRuleViolated, Severity: SeverityWarning}
	b.ev.Load([]Rule{b.rule}, []Finding{f})
	if got := summary(b.set("temp", 81.0)); got != "" {
		t.Errorf("still violated after start: %q", got)
	}
	if got := summary(b.set("temp", 70.0)); got != "-resolved" || b.store.ended[f.ID] != ReasonResolved {
		t.Errorf("resolve loaded finding: %q", got)
	}
}
