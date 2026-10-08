package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) LoadHealth(ctx context.Context) ([]evaluation.ElementHealth, error) {
	rows, err := s.q.ListElementHealth(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]evaluation.ElementHealth, len(rows))
	for i, r := range rows {
		out[i] = evaluation.ElementHealth{
			ElementID: r.ElementID,
			Health:    evaluation.Health(value(r.Health)),
		}
		if len(r.Rollup) > 0 {
			out[i].Rollup = new(evaluation.Rollup)
			if err := json.Unmarshal(r.Rollup, out[i].Rollup); err != nil {
				return nil, fmt.Errorf("element %s: rollup: %w", r.ElementID, err)
			}
		}
	}
	return out, nil
}

func (s *Store) SaveHealth(ctx context.Context, hs []evaluation.ElementHealth) error {
	if len(hs) == 0 {
		return nil
	}
	params := make([]db.UpsertElementHealthParams, len(hs))
	for i, h := range hs {
		params[i] = db.UpsertElementHealthParams{
			ElementID: h.ElementID,
			Health:    nullable(db.Health(h.Health)),
		}
		if h.Rollup != nil {
			b, err := json.Marshal(h.Rollup)
			if err != nil {
				return err
			}
			params[i].Rollup = b
		}
	}
	var err error
	s.q.UpsertElementHealth(ctx, params).Exec(func(i int, e error) {
		if err == nil && e != nil {
			err = fmt.Errorf("save health of %s: %w", params[i].ElementID, e)
		}
	})
	return err
}
