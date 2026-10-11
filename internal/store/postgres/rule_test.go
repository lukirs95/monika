//go:build integration

package postgres_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func newRule(scope uuid.UUID) evaluation.Rule {
	return evaluation.Rule{
		ID: newID(), Name: "Signal", Enabled: true, Severity: evaluation.SeverityError,
		ScopeElementID: scope, MatchDriver: "videoxlink",
		Fact: "signal_present", Op: evaluation.OpEq, Value: json.RawMessage(`true`),
		When:        &evaluation.Condition{Fact: "running", Op: evaluation.OpEq, Value: json.RawMessage(`true`)},
		HoldSeconds: 5, ClearHoldSeconds: 30,
	}
}

// activeFinding legt einen aktiven Befund der Regel am Element an und liefert seine ID.
func activeFinding(t *testing.T, pool *pgxpool.Pool, element, rule uuid.UUID) uuid.UUID {
	t.Helper()
	id := newID()
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO finding (id, element_id, element_path, origin, rule_id, code, severity, raised_at) VALUES ($1, $2, 'X8 / SDI 1', 'rule', $3, 'rule.violated', 'error', now())`,
		id, element, rule); err != nil {
		t.Fatalf("insert finding: %v", err)
	}
	return id
}

// findingEnd liefert, ob der Befund beendet ist, und den Grund.
func findingEnd(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (cleared bool, reason string, ruleID *uuid.UUID) {
	t.Helper()
	var r *string
	if err := pool.QueryRow(t.Context(),
		`SELECT cleared_at IS NOT NULL, details->>'cleared_reason', rule_id FROM finding WHERE id = $1`, id,
	).Scan(&cleared, &r, &ruleID); err != nil {
		t.Fatalf("query finding: %v", err)
	}
	return cleared, value(r), ruleID
}

func value[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func TestRules(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)

	device := newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	global := evaluation.Rule{ID: newID(), Name: "Temperatur", Enabled: true, Severity: evaluation.SeverityWarning,
		Fact: "vxl.cpu_temp", Op: evaluation.OpLte, Value: json.RawMessage(`80`), ClearValue: json.RawMessage(`75`)}
	scoped := newRule(device)
	for _, r := range []evaluation.Rule{global, scoped} {
		if err := s.CreateRule(ctx, user, r); err != nil {
			t.Fatalf("CreateRule %s: %v", r.Name, err)
		}
	}
	rules, err := s.ListRules(ctx)
	slices.SortFunc(rules, func(a, b evaluation.Rule) int { return slices.Compare(a.ID[:], b.ID[:]) })
	if err != nil || !reflect.DeepEqual(rules, []evaluation.Rule{global, scoped}) {
		t.Fatalf("ListRules = %+v, %v;\nwant %+v", rules, err, []evaluation.Rule{global, scoped})
	}

	t.Run("disable withdraws findings", func(t *testing.T) {
		f := activeFinding(t, pool, device, scoped.ID)
		disabled := scoped
		disabled.Enabled = false
		if err := s.UpdateRule(ctx, user, disabled); err != nil {
			t.Fatalf("UpdateRule: %v", err)
		}
		if cleared, reason, _ := findingEnd(t, pool, f); !cleared || reason != string(evaluation.ReasonRuleDisabled) {
			t.Fatalf("finding cleared = %v, reason %q; want withdrawn as rule_disabled", cleared, reason)
		}
	})

	t.Run("delete withdraws findings and keeps them", func(t *testing.T) {
		f := activeFinding(t, pool, device, global.ID)
		if err := s.DeleteRule(ctx, user, global.ID); err != nil {
			t.Fatalf("DeleteRule: %v", err)
		}
		cleared, reason, ruleID := findingEnd(t, pool, f)
		if !cleared || reason != string(evaluation.ReasonRuleDeleted) || ruleID != nil {
			t.Fatalf("finding cleared = %v, reason %q, rule %v; want withdrawn as rule_deleted with rule_id NULL", cleared, reason, ruleID)
		}
	})

	t.Run("unknown rule", func(t *testing.T) {
		if err := s.UpdateRule(ctx, user, newRule(uuid.Nil)); !errors.Is(err, evaluation.ErrNotFound) {
			t.Errorf("UpdateRule = %v, want ErrNotFound", err)
		}
		if err := s.DeleteRule(ctx, user, newID()); !errors.Is(err, evaluation.ErrNotFound) {
			t.Errorf("DeleteRule = %v, want ErrNotFound", err)
		}
	})

	verbs := []string{}
	for _, e := range auditLog(t, pool) {
		if e.ObjectType == "rule" {
			verbs = append(verbs, e.Verb)
		}
	}
	if !slices.Equal(verbs, []string{"create", "create", "update", "delete"}) {
		t.Errorf("rule audit = %v, want create, create, update, delete", verbs)
	}
}
