package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

// verifyJSON ist das gespeicherte Format von action_run.verify.
type verifyJSON struct {
	TimeoutMS  int64           `json:"timeout_ms"`
	Conditions []conditionJSON `json:"conditions"`
}

type conditionJSON struct {
	Fact  string          `json:"fact"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}

func (s *Store) CreateRuns(ctx context.Context, runs []action.Run) error {
	if len(runs) == 0 {
		return nil
	}
	params := make([]db.InsertRunParams, len(runs))
	for i, r := range runs {
		verify, err := marshalVerify(r.Verify)
		if err != nil {
			return err
		}
		requestedAt := r.RequestedAt
		if requestedAt.IsZero() {
			requestedAt = time.Now()
		}
		params[i] = db.InsertRunParams{
			ID: r.ID, ParentID: nullable(r.ParentID), PresetID: nullable(r.PresetID),
			PresetStepID: nullable(r.PresetStepID), ElementID: nullable(r.ElementID),
			SourceID: nullable(r.SourceID), Action: r.Action, Params: r.Params,
			Status: db.ActionStatus(r.Status), Critical: r.Critical, Verify: verify,
			RequestedByID: r.RequestedByID, RequestedAt: requestedAt,
		}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Eltern und Kinder dürfen in beliebiger Reihenfolge kommen.
		if err := q.DeferConstraints(ctx); err != nil {
			return err
		}
		var err error
		q.InsertRun(ctx, params).Exec(func(i int, e error) {
			if err == nil && e != nil {
				err = fmt.Errorf("insert run %s: %w", params[i].ID, e)
			}
		})
		return err
	})
}

func (s *Store) UpdateRun(ctx context.Context, r action.Run) error {
	n, err := s.q.UpdateRun(ctx, db.UpdateRunParams{
		ID:          r.ID,
		Status:      db.ActionStatus(r.Status),
		SourceID:    nullable(r.SourceID),
		Response:    r.Response,
		SentAt:      nullable(r.SentAt),
		RespondedAt: nullable(r.RespondedAt),
		FinishedAt:  nullable(r.FinishedAt),
	})
	return affected(n, err, action.ErrNotFound)
}

func (s *Store) FinishInterruptedRuns(ctx context.Context, reason string) (int, error) {
	n, err := s.q.FinishInterruptedRuns(ctx, reason)
	return int(n), err
}

func marshalVerify(v *action.Verify) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	out := verifyJSON{TimeoutMS: v.Timeout.Milliseconds(), Conditions: make([]conditionJSON, len(v.Conditions))}
	for i, c := range v.Conditions {
		out.Conditions[i] = conditionJSON{Fact: c.Fact, Op: string(c.Op), Value: c.Value}
	}
	return json.Marshal(out)
}
