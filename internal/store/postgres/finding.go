package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) ListActiveFindings(ctx context.Context) ([]evaluation.Finding, error) {
	rows, err := s.q.ListActiveFindings(ctx)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, toFinding), nil
}

func (s *Store) RaiseFinding(ctx context.Context, f evaluation.Finding) error {
	err := s.q.RaiseFinding(ctx, db.RaiseFindingParams{
		ID:         f.ID,
		ElementID:  f.ElementID,
		Origin:     db.FindingOrigin(f.Origin),
		RuleID:     nullable(f.RuleID),
		Code:       f.Code,
		Severity:   db.Severity(f.Severity),
		Message:    nullable(f.Message),
		Details:    f.Details,
		CausedByID: nullable(f.CausedByID),
		RaisedAt:   nullable(f.RaisedAt),
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
		pgErr.Code == pgerrcode.UniqueViolation && pgErr.ConstraintName == "finding_active_uq" {
		return evaluation.ErrAlreadyActive
	}
	return err
}

func (s *Store) EndFinding(ctx context.Context, id uuid.UUID, reason evaluation.ClearReason) error {
	n, err := s.q.EndFinding(ctx, db.EndFindingParams{ID: id, Reason: string(reason)})
	return affected(n, err, evaluation.ErrNotFound)
}

func (s *Store) SetFindingCause(ctx context.Context, id, causedBy uuid.UUID) error {
	n, err := s.q.SetFindingCause(ctx, db.SetFindingCauseParams{ID: id, CausedByID: nullable(causedBy)})
	return affected(n, err, evaluation.ErrNotFound)
}

func (s *Store) AckFinding(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	n, err := s.q.AckFinding(ctx, db.AckFindingParams{ID: id, AckedByID: nullable(actor)})
	return affected(n, err, evaluation.ErrNotFound)
}

func toFinding(f db.Finding) evaluation.Finding {
	return evaluation.Finding{
		ID:         f.ID,
		ElementID:  f.ElementID,
		Origin:     evaluation.Origin(f.Origin),
		RuleID:     value(f.RuleID),
		Code:       f.Code,
		Severity:   evaluation.Severity(f.Severity),
		Message:    value(f.Message),
		Details:    f.Details,
		CausedByID: value(f.CausedByID),
		RaisedAt:   f.RaisedAt,
		ClearedAt:  value(f.ClearedAt),
		AckedAt:    value(f.AckedAt),
		AckedByID:  value(f.AckedByID),
	}
}
