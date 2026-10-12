package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/history"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

// Verlauf lesen (internal/history): rein lesend, ohne Audit.

func (s *Store) ListAudit(ctx context.Context, f history.AuditFilter) ([]history.AuditEntry, error) {
	rows, err := s.q.ListAuditHistory(ctx, db.ListAuditHistoryParams{
		Before: nullable(f.Before), FromAt: nullable(f.From), ToAt: nullable(f.To),
		ObjectType: nullable(f.ObjectType), ObjectID: nullable(f.ObjectID), ActorID: nullable(f.Actor),
		SystemOnly: f.System, Verb: nullable(f.Verb), Lim: int32(f.Limit),
	})
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, func(a db.AuditLog) history.AuditEntry {
		return history.AuditEntry{
			ID: a.ID, ActorID: value(a.ActorID), At: a.At, Verb: a.Verb, ObjectType: a.ObjectType,
			ObjectID: value(a.ObjectID), Before: a.Before, After: a.After,
		}
	}), nil
}

func (s *Store) ListFindings(ctx context.Context, f history.FindingFilter) ([]evaluation.Finding, error) {
	rows, err := s.q.ListFindingHistory(ctx, db.ListFindingHistoryParams{
		Before: nullable(f.Before), FromAt: nullable(f.From), ToAt: nullable(f.To),
		AllVisible: f.All, Visible: nonNilIDs(f.Visible), AnyElement: f.Elements == nil, Elements: nonNilIDs(f.Elements),
		Code: nullable(f.Code), Severity: nullable(string(f.Severity)), Origin: nullable(string(f.Origin)),
		RuleID: nullable(f.RuleID), State: nullable(string(f.State)), Lim: int32(f.Limit),
	})
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, toFinding), nil
}

func (s *Store) ListRuns(ctx context.Context, f history.RunFilter) ([]action.Run, error) {
	rows, err := s.q.ListRunHistory(ctx, db.ListRunHistoryParams{
		Before: nullable(f.Before), FromAt: nullable(f.From), ToAt: nullable(f.To), Roots: f.Roots,
		AllVisible: f.All, Visible: nonNilIDs(f.Visible), AnyElement: f.Elements == nil, Elements: nonNilIDs(f.Elements),
		Status: nullable(string(f.Status)), Action: nullable(f.Action), PresetID: nullable(f.PresetID),
		RequestedBy: nullable(f.RequestedBy), Lim: int32(f.Limit),
	})
	if err != nil {
		return nil, err
	}
	return toRuns(rows)
}

// RunByID liefert einen Lauf mit seinen Kindern. Unbekannt: action.ErrNotFound.
func (s *Store) RunByID(ctx context.Context, id uuid.UUID) (action.Run, []action.Run, error) {
	r, err := s.q.GetRun(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return action.Run{}, nil, action.ErrNotFound
	} else if err != nil {
		return action.Run{}, nil, err
	}
	run, err := toRun(r)
	if err != nil {
		return action.Run{}, nil, err
	}
	rows, err := s.q.ListChildRuns(ctx, &id)
	if err != nil {
		return action.Run{}, nil, err
	}
	children, err := toRuns(rows)
	return run, children, err
}

func toRuns(rows []db.ActionRun) ([]action.Run, error) {
	out := make([]action.Run, 0, len(rows))
	for _, r := range rows {
		run, err := toRun(r)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

func toRun(r db.ActionRun) (action.Run, error) {
	run := action.Run{
		ID: r.ID, ParentID: value(r.ParentID), PresetID: value(r.PresetID), PresetStepID: value(r.PresetStepID),
		ElementID: value(r.ElementID), ElementPath: value(r.ElementPath), SourceID: value(r.SourceID),
		Action: r.Action, Params: r.Params, Status: action.Status(r.Status), Critical: r.Critical, Response: r.Response,
		RequestedByID: r.RequestedByID, RequestedAt: r.RequestedAt,
		SentAt: value(r.SentAt), RespondedAt: value(r.RespondedAt), FinishedAt: value(r.FinishedAt),
	}
	if len(r.Verify) > 0 && string(r.Verify) != "null" {
		var v verifyJSON
		if err := json.Unmarshal(r.Verify, &v); err != nil {
			return action.Run{}, fmt.Errorf("run %s: verify: %w", r.ID, err)
		}
		run.Verify = &action.Verify{Timeout: time.Duration(v.TimeoutMS) * time.Millisecond}
		for _, c := range v.Conditions {
			run.Verify.Conditions = append(run.Verify.Conditions, evaluation.Condition{Fact: c.Fact, Op: evaluation.Op(c.Op), Value: c.Value})
		}
	}
	return run, nil
}

// nonNilIDs: pgx schreibt eine nil-Liste als NULL; ANY(NULL) ist nie wahr, ein leeres Array auch
// nicht. Die Schalter all_visible und any_element entscheiden, ob gefiltert wird.
func nonNilIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}
