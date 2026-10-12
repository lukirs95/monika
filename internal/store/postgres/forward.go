package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/forward"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) ListTargets(ctx context.Context) ([]forward.Target, error) {
	rows, err := s.q.ListForwardTargets(ctx)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, toTarget), nil
}

func (s *Store) CreateTarget(ctx context.Context, actor uuid.UUID, t forward.Target) error {
	p := targetParams(t)
	return targetErr(s.q.CreateForwardTarget(ctx, db.CreateForwardTargetParams{
		ActorID: nullable(actor), ID: t.ID, Name: p.Name, Kind: p.Kind, Enabled: p.Enabled, Config: p.Config,
		SecretID: p.SecretID, Events: p.Events, MinSeverity: p.MinSeverity, ScopeElementID: p.ScopeElementID,
	}))
}

// targetErr übersetzt ein unbekanntes Secret in forward.ErrSecretNotFound.
func targetErr(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
		pgErr.Code == pgerrcode.ForeignKeyViolation && pgErr.ConstraintName == "forward_target_secret_id_fkey" {
		return forward.ErrSecretNotFound
	}
	return err
}

func (s *Store) UpdateTarget(ctx context.Context, actor uuid.UUID, t forward.Target) error {
	p := targetParams(t)
	p.ActorID, p.ID = nullable(actor), t.ID
	n, err := s.q.UpdateForwardTarget(ctx, p)
	return affected(n, targetErr(err), forward.ErrNotFound)
}

func (s *Store) DeleteTarget(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	n, err := s.q.DeleteForwardTarget(ctx, db.DeleteForwardTargetParams{ActorID: nullable(actor), ID: id})
	return affected(n, err, forward.ErrNotFound)
}

func (s *Store) SetTargetResults(ctx context.Context, rs []forward.Result) error {
	if len(rs) == 0 {
		return nil
	}
	params := mapSlice(rs, func(r forward.Result) db.SetForwardTargetResultParams {
		return db.SetForwardTargetResultParams{ID: r.TargetID, At: r.At, Error: nullable(r.Error)}
	})
	var err error
	s.q.SetForwardTargetResult(ctx, params).Exec(func(i int, e error) {
		if err == nil && e != nil {
			err = fmt.Errorf("set result of target %s: %w", params[i].ID, e)
		}
	})
	return err
}

// targetParams übersetzt ein Ziel in die Spalten von UpdateForwardTarget; Create nutzt dieselben Werte.
func targetParams(t forward.Target) db.UpdateForwardTargetParams {
	return db.UpdateForwardTargetParams{
		Name:           t.Name,
		Kind:           db.ForwardKind(t.Kind),
		Enabled:        t.Enabled,
		Config:         jsonObject(t.Config),
		SecretID:       nullable(t.SecretID),
		Events:         nonNil(t.Events),
		MinSeverity:    nullable(db.Severity(t.MinSeverity)),
		ScopeElementID: nullable(t.ScopeElementID),
	}
}

func toTarget(t db.ForwardTarget) forward.Target {
	return forward.Target{
		ID:             t.ID,
		Name:           t.Name,
		Kind:           forward.Kind(t.Kind),
		Enabled:        t.Enabled,
		Config:         t.Config,
		SecretID:       value(t.SecretID),
		Events:         t.Events,
		MinSeverity:    evaluation.Severity(value(t.MinSeverity)),
		ScopeElementID: value(t.ScopeElementID),
		LastSentAt:     value(t.LastSentAt),
		LastError:      value(t.LastError),
	}
}
