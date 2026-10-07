package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/store/postgres/db"
)

// ErrMemberCycle bedeutet, dass die Mitgliedschaft einen Zyklus zwischen Gruppen schließen würde.
var ErrMemberCycle = errors.New("member link would create a cycle")

// AddGroupMember nimmt ein Element in eine Gruppe auf. Lock, Zyklenprüfung und Insert laufen
// in einer Transaktion, damit parallele Änderungen keinen Zyklus erzeugen können.
func AddGroupMember(ctx context.Context, pool *pgxpool.Pool, arg db.CreateMemberLinkParams) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.LockMemberLinks(ctx); err != nil {
			return fmt.Errorf("lock member links: %w", err)
		}
		cycle, err := q.MemberLinkWouldCycle(ctx, db.MemberLinkWouldCycleParams{GroupID: arg.GroupID, MemberID: arg.MemberID})
		if err != nil {
			return fmt.Errorf("check member cycle: %w", err)
		}
		if cycle {
			return ErrMemberCycle
		}
		if err := q.CreateMemberLink(ctx, arg); err != nil {
			return fmt.Errorf("create member link: %w", err)
		}
		return nil
	})
}
