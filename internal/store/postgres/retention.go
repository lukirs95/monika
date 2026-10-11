package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) PurgeFindings(ctx context.Context, endedBefore time.Time, limit int) (int, error) {
	n, err := s.q.PurgeFindings(ctx, db.PurgeFindingsParams{EndedBefore: &endedBefore, Batch: int32(limit)})
	return int(n), err
}

func (s *Store) PurgeRuns(ctx context.Context, finishedBefore time.Time, limit int) (int, error) {
	n, err := s.q.PurgeRuns(ctx, db.PurgeRunsParams{FinishedBefore: &finishedBefore, Batch: int32(limit)})
	return int(n), err
}

func (s *Store) PurgeAudit(ctx context.Context, before time.Time, limit int) (int, error) {
	n, err := s.q.PurgeAudit(ctx, db.PurgeAuditParams{Before: before, Batch: int32(limit)})
	return int(n), err
}

// PurgeSessions löscht alte Sitzungen und benutzte Wiederherstellungscodes in einer Transaktion.
func (s *Store) PurgeSessions(ctx context.Context, before time.Time) (int, error) {
	total := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		sessions, err := q.PurgeSessions(ctx, before)
		if err != nil {
			return fmt.Errorf("purge sessions: %w", err)
		}
		codes, err := q.PurgeRecoveryCodes(ctx, &before)
		if err != nil {
			return fmt.Errorf("purge recovery codes: %w", err)
		}
		total = int(sessions + codes)
		return nil
	})
	return total, err
}
