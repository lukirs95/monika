package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lukirs95/monika/internal/secret"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) CreateSecret(ctx context.Context, actor uuid.UUID, sec secret.Secret, v secret.Encrypted) error {
	err := s.q.CreateSecret(ctx, db.CreateSecretParams{
		ActorID:     nullable(actor),
		ID:          sec.ID,
		Name:        sec.Name,
		Description: nullable(sec.Description),
		Ciphertext:  v.Ciphertext,
		Nonce:       v.Nonce,
		KeyID:       v.KeyID,
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
		pgErr.Code == pgerrcode.UniqueViolation && pgErr.ConstraintName == "secret_name_key" {
		return secret.ErrNameTaken
	}
	return err
}

func (s *Store) ListSecrets(ctx context.Context) ([]secret.Secret, error) {
	rows, err := s.q.ListSecrets(ctx)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, func(r db.ListSecretsRow) secret.Secret {
		return secret.Secret{
			ID:          r.ID,
			Name:        r.Name,
			Description: value(r.Description),
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		}
	}), nil
}

func (s *Store) SecretValue(ctx context.Context, id uuid.UUID) (secret.Encrypted, error) {
	r, err := s.q.GetSecretValue(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return secret.Encrypted{}, secret.ErrNotFound
	}
	if err != nil {
		return secret.Encrypted{}, err
	}
	return secret.Encrypted{ID: r.ID, Ciphertext: r.Ciphertext, Nonce: r.Nonce, KeyID: r.KeyID}, nil
}

func (s *Store) ListSecretValues(ctx context.Context) ([]secret.Encrypted, error) {
	rows, err := s.q.ListSecretValues(ctx)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, func(r db.ListSecretValuesRow) secret.Encrypted {
		return secret.Encrypted{ID: r.ID, Ciphertext: r.Ciphertext, Nonce: r.Nonce, KeyID: r.KeyID}
	}), nil
}

func (s *Store) ReplaceSecretValue(ctx context.Context, actor uuid.UUID, v secret.Encrypted) error {
	n, err := s.q.ReplaceSecretValue(ctx, db.ReplaceSecretValueParams{
		ActorID:    nullable(actor),
		ID:         v.ID,
		Ciphertext: v.Ciphertext,
		Nonce:      v.Nonce,
		KeyID:      v.KeyID,
	})
	return affected(n, err, secret.ErrNotFound)
}

func (s *Store) ReencryptSecret(ctx context.Context, v secret.Encrypted) error {
	n, err := s.q.ReencryptSecret(ctx, db.ReencryptSecretParams{
		ID:         v.ID,
		Ciphertext: v.Ciphertext,
		Nonce:      v.Nonce,
		KeyID:      v.KeyID,
	})
	return affected(n, err, secret.ErrNotFound)
}

func (s *Store) DeleteSecret(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	n, err := s.q.DeleteSecret(ctx, db.DeleteSecretParams{ActorID: nullable(actor), ID: id})
	// Fremdschlüssel mit ON DELETE RESTRICT melden in PostgreSQL 18 restrict_violation.
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.RestrictViolation {
		return secret.ErrInUse
	}
	return affected(n, err, secret.ErrNotFound)
}
