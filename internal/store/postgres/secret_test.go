//go:build integration

package postgres_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func TestSecretLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	q := db.New(server.NewDB(t))

	id := uuid.Must(uuid.NewV7())
	err := q.CreateSecret(ctx, db.CreateSecretParams{
		ID:         id,
		Name:       "xlink-admin",
		Ciphertext: []byte("cipher-1"),
		Nonce:      []byte("nonce-1"),
		KeyID:      "k1",
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	list, err := q.ListSecrets(ctx)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Name != "xlink-admin" {
		t.Fatalf("ListSecrets = %+v, want one secret xlink-admin", list)
	}

	n, err := q.ReplaceSecretValue(ctx, db.ReplaceSecretValueParams{
		ID:         id,
		Ciphertext: []byte("cipher-2"),
		Nonce:      []byte("nonce-2"),
		KeyID:      "k2",
	})
	if err != nil || n != 1 {
		t.Fatalf("ReplaceSecretValue = %d, %v; want 1, nil", n, err)
	}

	got, err := q.GetSecretValue(ctx, id)
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}
	if !bytes.Equal(got.Ciphertext, []byte("cipher-2")) || got.KeyID != "k2" {
		t.Fatalf("GetSecretValue = %+v, want replaced value with key k2", got)
	}

	n, err = q.DeleteSecret(ctx, id)
	if err != nil || n != 1 {
		t.Fatalf("DeleteSecret = %d, %v; want 1, nil", n, err)
	}
}

func TestDeleteSecretInUse(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	q := db.New(pool)

	id := uuid.Must(uuid.NewV7())
	err := q.CreateSecret(ctx, db.CreateSecretParams{
		ID:         id,
		Name:       "webhook-token",
		Ciphertext: []byte("cipher"),
		Nonce:      []byte("nonce"),
		KeyID:      "k1",
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO forward_target (name, kind, events, secret_id) VALUES ('hook', 'webhook', '{}', $1)`, id)
	if err != nil {
		t.Fatalf("insert forward_target: %v", err)
	}

	_, err = q.DeleteSecret(ctx, id)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.RestrictViolation {
		t.Fatalf("DeleteSecret error = %v, want restrict violation", err)
	}
}
