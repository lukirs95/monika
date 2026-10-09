package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/secret"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) TOTP(ctx context.Context, user uuid.UUID) (auth.TOTP, error) {
	t, err := s.q.GetTOTP(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.TOTP{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.TOTP{}, err
	}
	return auth.TOTP{
		Value:       secret.Encrypted{ID: t.UserID, Ciphertext: t.Ciphertext, Nonce: t.Nonce, KeyID: t.KeyID},
		ConfirmedAt: value(t.ConfirmedAt),
		LastStep:    value(t.LastStep),
	}, nil
}

func (s *Store) SaveTOTP(ctx context.Context, user uuid.UUID, v secret.Encrypted) error {
	n, err := s.q.SaveTOTP(ctx, db.SaveTOTPParams{UserID: user, Ciphertext: v.Ciphertext, Nonce: v.Nonce, KeyID: v.KeyID})
	return affected(n, err, auth.ErrTOTPConfirmed)
}

func (s *Store) ConfirmTOTP(ctx context.Context, user uuid.UUID, step int64, recoveryCodeHashes []string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		n, err := q.ConfirmTOTP(ctx, db.ConfirmTOTPParams{UserID: user, Step: &step})
		if err := affected(n, err, auth.ErrNotFound); err != nil {
			return err
		}
		if err := replaceRecoveryCodes(ctx, q, user, recoveryCodeHashes); err != nil {
			return err
		}
		return q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
			ActorID: &user, Verb: "update", ObjectType: "app_user", ObjectID: user, After: []byte(`{"totp": "enabled"}`),
		})
	})
}

func (s *Store) UseTOTPStep(ctx context.Context, user uuid.UUID, step int64) (bool, error) {
	n, err := s.q.UseTOTPStep(ctx, db.UseTOTPStepParams{UserID: user, Step: &step})
	return n == 1, err
}

func (s *Store) UseRecoveryCode(ctx context.Context, user uuid.UUID, codeHash string) (bool, error) {
	n, err := s.q.UseRecoveryCode(ctx, db.UseRecoveryCodeParams{UserID: user, CodeHash: codeHash})
	return n == 1, err
}

func (s *Store) ResetTOTP(ctx context.Context, actor uuid.UUID, user uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		n, err := q.DeleteTOTP(ctx, user)
		if err := affected(n, err, auth.ErrNotFound); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, user); err != nil {
			return err
		}
		return q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
			ActorID: nullable(actor), Verb: "update", ObjectType: "app_user", ObjectID: user, After: []byte(`{"totp": "reset"}`),
		})
	})
}

func replaceRecoveryCodes(ctx context.Context, q *db.Queries, user uuid.UUID, hashes []string) error {
	if err := q.DeleteRecoveryCodes(ctx, user); err != nil {
		return err
	}
	var err error
	q.InsertRecoveryCode(ctx, mapSlice(hashes, func(h string) db.InsertRecoveryCodeParams {
		return db.InsertRecoveryCodeParams{UserID: user, CodeHash: h}
	})).Exec(func(i int, e error) {
		if err == nil && e != nil {
			err = fmt.Errorf("insert recovery code %d: %w", i, e)
		}
	})
	return err
}

// TOTPVault macht die TOTP-Geheimnisse zu einem secret.Vault, damit VerifyAndRotate sie beim Start
// zusammen mit den Secrets prüft und rotiert.
type TOTPVault struct{ store *Store }

var _ secret.Vault = TOTPVault{}

// TOTPVault liefert den Vault der TOTP-Geheimnisse für secret.NewService.
func (s *Store) TOTPVault() TOTPVault { return TOTPVault{store: s} }

func (v TOTPVault) ListEncrypted(ctx context.Context) ([]secret.Encrypted, error) {
	rows, err := v.store.q.ListTOTPValues(ctx)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, func(r db.ListTOTPValuesRow) secret.Encrypted {
		return secret.Encrypted{ID: r.UserID, Ciphertext: r.Ciphertext, Nonce: r.Nonce, KeyID: r.KeyID}
	}), nil
}

func (v TOTPVault) Reencrypt(ctx context.Context, e secret.Encrypted) error {
	n, err := v.store.q.ReencryptTOTP(ctx, db.ReencryptTOTPParams{
		UserID: e.ID, Ciphertext: e.Ciphertext, Nonce: e.Nonce, KeyID: e.KeyID,
	})
	return affected(n, err, secret.ErrNotFound)
}
