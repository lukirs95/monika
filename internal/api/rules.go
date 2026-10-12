package api

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
)

// Regeln und Quittierung über Core.Do. Eine Regel ist sichtbar, wenn ihr Geltungsbereich es ist;
// globale Regeln sieht jeder.

// ruleVisible meldet, ob der Benutzer den Geltungsbereich einer Regel sieht.
func (s *Server) ruleVisible(v *core.View, p auth.Principal, scope uuid.UUID) bool {
	return scope == uuid.Nil || s.visible(v, p, scope)
}

func (s *Server) ListRules(ctx context.Context, _ oapi.ListRulesRequestObject) (oapi.ListRulesResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	out := oapi.ListRules200JSONResponse{}
	for r := range v.Rules.All() {
		if s.ruleVisible(v, p, r.ScopeElementID) {
			out = append(out, toRule(r))
		}
	}
	return out, nil
}

func (s *Server) CreateRule(ctx context.Context, rq oapi.CreateRuleRequestObject) (oapi.CreateRuleResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	r := fromRuleInput(newID(), *rq.Body)
	if !s.ruleVisible(v, p, r.ScopeElementID) {
		return nil, errNotFound
	}
	if err := s.require(v, p, auth.PermRuleEdit, r.ScopeElementID); err != nil {
		return nil, err
	}
	if v, err = s.do(ctx, core.CreateRule{Actor: p.UserID, Rule: r}); err != nil {
		return nil, err
	}
	created, ok := v.Rules.Rule(r.ID)
	if !ok {
		return nil, errNotFound
	}
	return oapi.CreateRule201JSONResponse(toRule(created)), nil
}

// rule liefert eine Regel, deren Geltungsbereich der Benutzer sieht und an dem er rule_edit hat.
func (s *Server) rule(v *core.View, p auth.Principal, id uuid.UUID) (evaluation.Rule, error) {
	r, ok := v.Rules.Rule(id)
	if !ok || !s.ruleVisible(v, p, r.ScopeElementID) {
		return evaluation.Rule{}, errNotFound
	}
	return r, s.require(v, p, auth.PermRuleEdit, r.ScopeElementID)
}

func (s *Server) UpdateRule(ctx context.Context, rq oapi.UpdateRuleRequestObject) (oapi.UpdateRuleResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.rule(v, p, rq.RuleID); err != nil {
		return nil, err
	}
	r := fromRuleInput(rq.RuleID, *rq.Body)
	if !s.ruleVisible(v, p, r.ScopeElementID) {
		return nil, errNotFound
	}
	if err := s.require(v, p, auth.PermRuleEdit, r.ScopeElementID); err != nil {
		return nil, err
	}
	if v, err = s.do(ctx, core.UpdateRule{Actor: p.UserID, Rule: r}); err != nil {
		return nil, err
	}
	updated, ok := v.Rules.Rule(r.ID)
	if !ok {
		return nil, errNotFound
	}
	return oapi.UpdateRule200JSONResponse(toRule(updated)), nil
}

func (s *Server) DeleteRule(ctx context.Context, rq oapi.DeleteRuleRequestObject) (oapi.DeleteRuleResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.rule(v, p, rq.RuleID); err != nil {
		return nil, err
	}
	if _, err := s.do(ctx, core.DeleteRule{Actor: p.UserID, ID: rq.RuleID}); err != nil {
		return nil, err
	}
	return oapi.DeleteRule204Response{}, nil
}

func (s *Server) AckFinding(ctx context.Context, rq oapi.AckFindingRequestObject) (oapi.AckFindingResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	f, ok := v.Findings[rq.FindingID]
	if !ok || !s.visible(v, p, f.ElementID) {
		return nil, errNotFound
	}
	if err := s.require(v, p, auth.PermAcknowledge, f.ElementID); err != nil {
		return nil, err
	}
	if _, err := s.do(ctx, core.AckFinding{Actor: p.UserID, ID: rq.FindingID}); err != nil {
		return nil, err
	}
	return oapi.AckFinding204Response{}, nil
}

func fromRuleInput(id uuid.UUID, in oapi.RuleInput) evaluation.Rule {
	r := evaluation.Rule{
		ID: id, Name: in.Name, Enabled: in.Enabled, Severity: evaluation.Severity(in.Severity),
		ScopeElementID: valueID(in.ScopeElementID), MatchKind: value(in.MatchKind),
		MatchTransport: value(in.MatchTransport), MatchDriver: value(in.MatchDriver),
		Fact: in.Fact, Op: evaluation.Op(in.Op), Value: in.Value,
		HoldSeconds: in.HoldSeconds, ClearHoldSeconds: value(in.ClearHoldSeconds),
	}
	if w := in.When; w != nil {
		r.When = &evaluation.Condition{Fact: w.Fact, Op: evaluation.Op(w.Op), Value: w.Value}
	}
	if in.ClearValue != nil {
		r.ClearValue = strconv.AppendFloat(nil, *in.ClearValue, 'g', -1, 64)
	}
	return r
}

func toRule(r evaluation.Rule) oapi.Rule {
	out := oapi.Rule{
		ID: r.ID, Name: r.Name, Enabled: r.Enabled, Severity: oapi.RuleSeverity(r.Severity),
		ScopeElementID: optionalID(r.ScopeElementID), MatchKind: optional(r.MatchKind),
		MatchTransport: optional(r.MatchTransport), MatchDriver: optional(r.MatchDriver),
		Fact: r.Fact, Op: oapi.RuleOp(r.Op), Value: r.Value,
		HoldSeconds: r.HoldSeconds, ClearHoldSeconds: new(r.ClearHoldSeconds),
	}
	if w := r.When; w != nil {
		out.When = &oapi.Condition{Fact: w.Fact, Op: oapi.ConditionOp(w.Op), Value: w.Value}
	}
	var clear float64
	if len(r.ClearValue) > 0 && json.Unmarshal(r.ClearValue, &clear) == nil {
		out.ClearValue = &clear
	}
	return out
}
