//go:build integration

package postgres_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/secret"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func encrypted(id uuid.UUID, ciphertext, keyID string) secret.Encrypted {
	return secret.Encrypted{ID: id, Ciphertext: []byte(ciphertext), Nonce: []byte("nonce-123456"), KeyID: keyID}
}

func TestSecretLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)

	id := newID()
	if err := s.CreateSecret(ctx, user, secret.Secret{ID: id, Name: "xlink-admin"}, encrypted(id, "cipher-1", "k1")); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	other := newID()
	if err := s.CreateSecret(ctx, user, secret.Secret{ID: other, Name: "xlink-admin"}, encrypted(other, "x", "k1")); !errors.Is(err, secret.ErrNameTaken) {
		t.Fatalf("CreateSecret with taken name = %v, want ErrNameTaken", err)
	}

	list, err := s.ListSecrets(ctx)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Name != "xlink-admin" {
		t.Fatalf("ListSecrets = %+v, want one secret xlink-admin", list)
	}

	if err := s.ReplaceSecretValue(ctx, user, encrypted(id, "cipher-2", "k2")); err != nil {
		t.Fatalf("ReplaceSecretValue: %v", err)
	}
	got, err := s.SecretValue(ctx, id)
	if err != nil || !bytes.Equal(got.Ciphertext, []byte("cipher-2")) || got.KeyID != "k2" {
		t.Fatalf("SecretValue = %+v, %v; want replaced value with key k2", got, err)
	}
	if err := s.ReencryptSecret(ctx, encrypted(id, "cipher-3", "k3")); err != nil {
		t.Fatalf("ReencryptSecret: %v", err)
	}
	if err := s.DeleteSecret(ctx, user, id); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	if _, err := s.SecretValue(ctx, id); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("SecretValue after delete = %v, want ErrNotFound", err)
	}
	if err := s.ReplaceSecretValue(ctx, user, encrypted(id, "x", "k1")); !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("ReplaceSecretValue after delete = %v, want ErrNotFound", err)
	}

	// Audit: create, update, delete; die Rotation schreibt nichts, und nie steht ein Wert darin.
	verbs := []string{}
	for _, e := range auditLog(t, pool) {
		verbs = append(verbs, e.Verb)
		if strings.Contains(string(e.Before)+string(e.After), "cipher") {
			t.Errorf("audit entry %+v contains the value", e)
		}
	}
	if strings.Join(verbs, ",") != "create,update,delete" {
		t.Fatalf("audit verbs = %v, want create,update,delete", verbs)
	}
}

func TestDeleteSecretInUse(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)

	id := newID()
	if err := s.CreateSecret(ctx, uuid.Nil, secret.Secret{ID: id, Name: "webhook-token"}, encrypted(id, "cipher", "k1")); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO forward_target (name, kind, events, secret_id) VALUES ('hook', 'webhook', '{}', $1)`, id)
	if err != nil {
		t.Fatalf("insert forward_target: %v", err)
	}

	if err := s.DeleteSecret(ctx, uuid.Nil, id); !errors.Is(err, secret.ErrInUse) {
		t.Fatalf("DeleteSecret = %v, want ErrInUse", err)
	}
}

func TestSecretServiceOnPostgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := postgres.New(server.NewDB(t))
	key := func() secret.Key {
		b := make([]byte, 32)
		rand.Read(b)
		k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(b))
		if err != nil {
			t.Fatalf("ParseKey: %v", err)
		}
		return k
	}
	oldKey, newKey := key(), key()

	old, err := secret.NewService(store, secret.Keys{Current: oldKey}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	sec, err := old.Create(ctx, uuid.Nil, "xlink-admin", "", json.RawMessage(`{"password": "geheim"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rotated, err := secret.NewService(store, secret.Keys{Current: newKey, Previous: oldKey}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if n, err := rotated.VerifyAndRotate(ctx); err != nil || n != 1 {
		t.Fatalf("VerifyAndRotate = %d, %v; want 1, nil", n, err)
	}
	got, err := rotated.Value(ctx, sec.ID)
	if err != nil || string(got) != `{"password": "geheim"}` {
		t.Fatalf("Value = %s, %v; want original value", got, err)
	}
}

// Die TOTP-Geheimnisse werden beim Start zusammen mit den Secrets rotiert.
func TestRotationIncludesTOTP(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	store := postgres.New(pool)
	anna := newUser(t, pool)
	key := func() secret.Key {
		b := make([]byte, 32)
		rand.Read(b)
		k, err := secret.ParseKey(base64.StdEncoding.EncodeToString(b))
		if err != nil {
			t.Fatalf("ParseKey: %v", err)
		}
		return k
	}
	oldKey, newKey := key(), key()

	old, err := secret.NewService(store, secret.Keys{Current: oldKey}, slog.New(slog.DiscardHandler), store.TOTPVault())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	seed, err := old.Seal(anna, []byte("totp-seed"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := store.SaveTOTP(ctx, anna, seed); err != nil {
		t.Fatalf("SaveTOTP: %v", err)
	}
	if _, err := old.Create(ctx, uuid.Nil, "xlink-admin", "", json.RawMessage(`{"password": "geheim"}`)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rotated, err := secret.NewService(store, secret.Keys{Current: newKey, Previous: oldKey}, slog.New(slog.DiscardHandler), store.TOTPVault())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if n, err := rotated.VerifyAndRotate(ctx); err != nil || n != 2 {
		t.Fatalf("VerifyAndRotate = %d, %v; want secret and TOTP rotated", n, err)
	}
	totp, err := store.TOTP(ctx, anna)
	if err != nil || totp.Value.KeyID != newKey.ID() {
		t.Fatalf("TOTP = %+v, %v; want new key", totp, err)
	}
	if got, err := rotated.Open(totp.Value); err != nil || string(got) != "totp-seed" {
		t.Fatalf("Open = %q, %v; want totp-seed", got, err)
	}
}
