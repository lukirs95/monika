package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) ListRules(ctx context.Context) ([]evaluation.Rule, error) {
	rows, err := s.q.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, toRule), nil
}

func (s *Store) CreateRule(ctx context.Context, actor uuid.UUID, r evaluation.Rule) error {
	p := ruleParams(r)
	return s.q.CreateRule(ctx, db.CreateRuleParams{
		ActorID: nullable(actor), ID: r.ID, Name: p.Name, Enabled: p.Enabled, Severity: p.Severity,
		ScopeElementID: p.ScopeElementID, MatchKind: p.MatchKind, MatchTransport: p.MatchTransport, MatchDriver: p.MatchDriver,
		Fact: p.Fact, Op: p.Op, Value: p.Value, WhenFact: p.WhenFact, WhenOp: p.WhenOp, WhenValue: p.WhenValue,
		ClearValue: p.ClearValue, HoldSeconds: p.HoldSeconds, ClearHoldSeconds: p.ClearHoldSeconds,
	})
}

func (s *Store) UpdateRule(ctx context.Context, actor uuid.UUID, r evaluation.Rule) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		p := ruleParams(r)
		p.ActorID, p.ID = nullable(actor), r.ID
		n, err := q.UpdateRule(ctx, p)
		if err := affected(n, err, evaluation.ErrNotFound); err != nil {
			return err
		}
		if !r.Enabled {
			if _, err := q.WithdrawRuleFindings(ctx, db.WithdrawRuleFindingsParams{
				RuleID: r.ID, Reason: string(evaluation.ReasonRuleDisabled),
			}); err != nil {
				return fmt.Errorf("withdraw findings: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) DeleteRule(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Eigenes Statement vor dem Löschen: Das Löschen setzt finding.rule_id auf NULL.
		if _, err := q.WithdrawRuleFindings(ctx, db.WithdrawRuleFindingsParams{
			RuleID: id, Reason: string(evaluation.ReasonRuleDeleted),
		}); err != nil {
			return fmt.Errorf("withdraw findings: %w", err)
		}
		n, err := q.DeleteRule(ctx, db.DeleteRuleParams{ActorID: nullable(actor), ID: id})
		return affected(n, err, evaluation.ErrNotFound)
	})
}

// ruleParams übersetzt eine Regel in die Spalten von UpdateRule; CreateRule nutzt dieselben Werte.
func ruleParams(r evaluation.Rule) db.UpdateRuleParams {
	p := db.UpdateRuleParams{
		Name:             r.Name,
		Enabled:          r.Enabled,
		Severity:         db.Severity(r.Severity),
		ScopeElementID:   nullable(r.ScopeElementID),
		MatchKind:        nullable(r.MatchKind),
		MatchTransport:   nullable(r.MatchTransport),
		MatchDriver:      nullable(r.MatchDriver),
		Fact:             r.Fact,
		Op:               db.RuleOp(r.Op),
		Value:            r.Value,
		ClearValue:       r.ClearValue,
		HoldSeconds:      int32(r.HoldSeconds),
		ClearHoldSeconds: int32(r.ClearHoldSeconds),
	}
	if w := r.When; w != nil {
		p.WhenFact, p.WhenOp, p.WhenValue = &w.Fact, new(db.RuleOp(w.Op)), w.Value
	}
	return p
}

func toRule(r db.Rule) evaluation.Rule {
	out := evaluation.Rule{
		ID:               r.ID,
		Name:             r.Name,
		Enabled:          r.Enabled,
		Severity:         evaluation.Severity(r.Severity),
		ScopeElementID:   value(r.ScopeElementID),
		MatchKind:        value(r.MatchKind),
		MatchTransport:   value(r.MatchTransport),
		MatchDriver:      value(r.MatchDriver),
		Fact:             r.Fact,
		Op:               evaluation.Op(r.Op),
		Value:            r.Value,
		ClearValue:       r.ClearValue,
		HoldSeconds:      int(r.HoldSeconds),
		ClearHoldSeconds: int(r.ClearHoldSeconds),
	}
	if r.WhenFact != nil {
		out.When = &evaluation.Condition{Fact: *r.WhenFact, Op: evaluation.Op(value(r.WhenOp)), Value: r.WhenValue}
	}
	return out
}
