package api

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/history"
)

// Verlauf (api.md, Filter im Verlauf): rein lesend aus PostgreSQL, sichtbar nach den Rechten
// (E8); Einträge zu gelöschten Elementen nur mit view global.

const defaultLimit = 100

// page übersetzt Cursor und Zeitraum; gelesen wird ein Eintrag mehr, um zu wissen, ob es eine
// nächste Seite gibt.
func page(before *uuid.UUID, limit *int, from, to *time.Time) history.Page {
	n := defaultLimit
	if limit != nil {
		n = *limit
	}
	return history.Page{Before: valueID(before), Limit: n + 1, From: value(from), To: value(to)}
}

// cut kürzt auf die Seite und liefert den Cursor der nächsten.
func cut[T any](items []T, p history.Page, id func(T) uuid.UUID) ([]T, *uuid.UUID) {
	if len(items) < p.Limit {
		return items, nil
	}
	items = items[:p.Limit-1]
	return items, new(id(items[len(items)-1]))
}

// historySight ist die Sichtbarkeit für den Verlauf mit dem Filter element (recursive wie
// View.InScope). Unsichtbare Elemente: 404; gelöschte nur mit view global und ohne recursive.
func (s *Server) historySight(v *core.View, p auth.Principal, element *uuid.UUID, recursive *bool) (history.Sight, error) {
	si := s.sight(v, p)
	out := history.Sight{All: si.all}
	if !si.all {
		out.Visible = slices.Collect(maps.Keys(si.visible))
	}
	if element == nil {
		return out, nil
	}
	_, exists := v.Graph.Element(*element)
	switch {
	case exists && !si.sees(*element), !exists && !si.all:
		return history.Sight{}, errNotFound
	case !exists && value(recursive):
		out.Elements = []uuid.UUID{} // recursive findet nur Elemente, die noch existieren
	case value(recursive):
		out.Elements = slices.Collect(v.Graph.ScopeElements(*element))
	default:
		out.Elements = []uuid.UUID{*element}
	}
	return out, nil
}

// deleted meldet, ob das Element eines Eintrags nicht mehr existiert.
func deleted(v *core.View, element uuid.UUID) *bool {
	_, ok := v.Graph.Element(element)
	return new(element != uuid.Nil && !ok)
}

func (s *Server) ListAudit(ctx context.Context, rq oapi.ListAuditRequestObject) (oapi.ListAuditResponseObject, error) {
	if _, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	pr := rq.Params
	f := history.AuditFilter{
		Page: page(pr.Before, pr.Limit, pr.From, pr.To), ObjectType: value(pr.ObjectType), ObjectID: valueID(pr.ObjectID),
	}
	if pr.Verb != nil {
		f.Verb = string(*pr.Verb)
	}
	switch actor := value(pr.Actor); actor {
	case "":
	case "monika":
		f.System = true
	default:
		id, err := uuid.Parse(actor)
		if err != nil {
			return nil, problem(http.StatusBadRequest, "invalid_request", "query parameter actor: a user ID or monika")
		}
		f.Actor = id
	}
	entries, err := s.cfg.History.ListAudit(ctx, f)
	if err != nil {
		return nil, err
	}
	entries, next := cut(entries, f.Page, func(e history.AuditEntry) uuid.UUID { return e.ID })
	out := oapi.ListAudit200JSONResponse{Items: []oapi.AuditEntry{}, NextBefore: next}
	for _, e := range entries {
		out.Items = append(out.Items, oapi.AuditEntry{
			ID: e.ID, ActorID: optionalID(e.ActorID), At: e.At.UTC(), Verb: e.Verb, ObjectType: e.ObjectType,
			ObjectID: optionalID(e.ObjectID), Before: e.Before, After: e.After,
		})
	}
	return out, nil
}

func (s *Server) ListFindingHistory(ctx context.Context, rq oapi.ListFindingHistoryRequestObject) (oapi.ListFindingHistoryResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	pr := rq.Params
	sight, err := s.historySight(v, p, pr.Element, pr.Recursive)
	if err != nil {
		return nil, err
	}
	f := history.FindingFilter{Page: page(pr.Before, pr.Limit, pr.From, pr.To), Sight: sight, Code: value(pr.Code), RuleID: valueID(pr.Rule)}
	if pr.Severity != nil {
		f.Severity = evaluation.Severity(*pr.Severity)
	}
	if pr.Origin != nil {
		f.Origin = evaluation.Origin(*pr.Origin)
	}
	if pr.State != nil {
		f.State = history.FindingState(*pr.State)
	}
	findings, err := s.cfg.History.ListFindings(ctx, f)
	if err != nil {
		return nil, err
	}
	findings, next := cut(findings, f.Page, func(f evaluation.Finding) uuid.UUID { return f.ID })
	out := oapi.ListFindingHistory200JSONResponse{Items: []oapi.Finding{}, NextBefore: next}
	for _, fd := range findings {
		item := toFinding(fd)
		item.ElementDeleted = deleted(v, fd.ElementID)
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *Server) ListRunHistory(ctx context.Context, rq oapi.ListRunHistoryRequestObject) (oapi.ListRunHistoryResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	pr := rq.Params
	sight, err := s.historySight(v, p, pr.Element, pr.Recursive)
	if err != nil {
		return nil, err
	}
	f := history.RunFilter{
		Page: page(pr.Before, pr.Limit, pr.From, pr.To), Sight: sight, Roots: s.seesPresets(v, p),
		Action: value(pr.Action), PresetID: valueID(pr.Preset), RequestedBy: valueID(pr.RequestedBy),
	}
	if pr.Status != nil {
		f.Status = action.Status(*pr.Status)
	}
	runs, err := s.cfg.History.ListRuns(ctx, f)
	if err != nil {
		return nil, err
	}
	runs, next := cut(runs, f.Page, func(r action.Run) uuid.UUID { return r.ID })
	out := oapi.ListRunHistory200JSONResponse{Items: []oapi.RunSummary{}, NextBefore: next}
	for _, r := range runs {
		item := toRunSummary(r)
		item.ElementDeleted = deleted(v, r.ElementID)
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// storedRun liefert einen beendeten Lauf aus dem Verlauf, den der Benutzer sieht: sein Ziel ist
// sichtbar, ein gelöschtes nur mit view global; Wurzeln von Presets mit Rechten an Presets.
func (s *Server) storedRun(ctx context.Context, v *core.View, p auth.Principal, id uuid.UUID) (action.Run, []action.Run, error) {
	r, children, err := s.cfg.History.RunByID(ctx, id)
	if err != nil {
		return action.Run{}, nil, err
	}
	if _, exists := v.Graph.Element(r.ElementID); r.ElementID != uuid.Nil && !exists {
		if !s.sight(v, p).all {
			return action.Run{}, nil, errNotFound
		}
	} else if !s.seesRun(v, p, r) {
		return action.Run{}, nil, errNotFound
	}
	return r, children, nil
}
