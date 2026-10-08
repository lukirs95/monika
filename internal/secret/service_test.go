package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// memStore ist eine In-Memory-Attrappe von Store.
type memStore struct {
	secrets map[uuid.UUID]Secret
	values  map[uuid.UUID]Encrypted
}

func newMemStore() *memStore {
	return &memStore{secrets: map[uuid.UUID]Secret{}, values: map[uuid.UUID]Encrypted{}}
}

func (m *memStore) CreateSecret(_ context.Context, _ uuid.UUID, s Secret, v Encrypted) error {
	for _, other := range m.secrets {
		if other.Name == s.Name {
			return ErrNameTaken
		}
	}
	m.secrets[s.ID], m.values[s.ID] = s, v
	return nil
}

func (m *memStore) ListSecrets(context.Context) ([]Secret, error) {
	var out []Secret
	for _, s := range m.secrets {
		out = append(out, s)
	}
	return out, nil
}

func (m *memStore) SecretValue(_ context.Context, id uuid.UUID) (Encrypted, error) {
	v, ok := m.values[id]
	if !ok {
		return Encrypted{}, ErrNotFound
	}
	return v, nil
}

func (m *memStore) ListSecretValues(context.Context) ([]Encrypted, error) {
	var out []Encrypted
	for _, v := range m.values {
		out = append(out, v)
	}
	return out, nil
}

func (m *memStore) ReplaceSecretValue(_ context.Context, _ uuid.UUID, v Encrypted) error {
	return m.ReencryptSecret(context.Background(), v)
}

func (m *memStore) ReencryptSecret(_ context.Context, v Encrypted) error {
	if _, ok := m.values[v.ID]; !ok {
		return ErrNotFound
	}
	m.values[v.ID] = v
	return nil
}

func (m *memStore) DeleteSecret(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
	if _, ok := m.values[id]; !ok {
		return ErrNotFound
	}
	delete(m.secrets, id)
	delete(m.values, id)
	return nil
}

func newTestService(t *testing.T, store Store, keys Keys) (*Service, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	s, err := NewService(store, keys, slog.New(slog.NewTextHandler(&log, nil)))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s, &log
}

func TestServiceCreateReplaceValue(t *testing.T) {
	ctx := t.Context()
	store := newMemStore()
	s, _ := newTestService(t, store, Keys{Current: newTestKey(t)})

	sec, err := s.Create(ctx, uuid.Nil, "xlink-admin", "", json.RawMessage(`{"password": "geheim"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if bytes.Contains(store.values[sec.ID].Ciphertext, []byte("geheim")) {
		t.Fatal("store holds the plaintext")
	}
	if err := s.Replace(ctx, uuid.Nil, sec.ID, json.RawMessage(`{"password": "neu"}`)); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err := s.Value(ctx, sec.ID)
	if err != nil || string(got) != `{"password": "neu"}` {
		t.Fatalf("Value = %s, %v; want replaced value", got, err)
	}

	invalid := []struct {
		name string
		err  error
		want error
	}{
		{"empty name", func() error { _, err := s.Create(ctx, uuid.Nil, "", "", json.RawMessage(`{}`)); return err }(), ErrInvalidName},
		{"create with invalid JSON", func() error { _, err := s.Create(ctx, uuid.Nil, "x", "", json.RawMessage(`kein json`)); return err }(), ErrInvalidValue},
		{"replace with invalid JSON", s.Replace(ctx, uuid.Nil, sec.ID, json.RawMessage(`{`)), ErrInvalidValue},
		{"taken name", func() error { _, err := s.Create(ctx, uuid.Nil, "xlink-admin", "", json.RawMessage(`{}`)); return err }(), ErrNameTaken},
	}
	for _, tt := range invalid {
		if !errors.Is(tt.err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, tt.err, tt.want)
		}
	}
}

func TestNewServiceWithoutKey(t *testing.T) {
	if _, err := NewService(newMemStore(), Keys{}, slog.Default()); err == nil {
		t.Fatal("NewService without current key succeeded, want error")
	}
}

func TestVerifyAndRotate(t *testing.T) {
	ctx := t.Context()
	oldKey, newKey := newTestKey(t), newTestKey(t)
	store := newMemStore()

	// Zwei Secrets, verschlüsselt mit dem alten Schlüssel.
	before, _ := newTestService(t, store, Keys{Current: oldKey})
	var ids []uuid.UUID
	for _, name := range []string{"a", "b"} {
		sec, err := before.Create(ctx, uuid.Nil, name, "", json.RawMessage(`{"token": "`+name+`"}`))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, sec.ID)
	}

	s, log := newTestService(t, store, Keys{Current: newKey, Previous: oldKey})
	rotated, err := s.VerifyAndRotate(ctx)
	if err != nil || rotated != 2 {
		t.Fatalf("VerifyAndRotate = %d, %v; want 2, nil", rotated, err)
	}
	for _, id := range ids {
		if store.values[id].KeyID != newKey.ID() {
			t.Errorf("secret %s still uses key %s", id, store.values[id].KeyID)
		}
	}

	// Zweiter Start: nichts mehr zu rotieren, Hinweis zum Entfernen des alten Schlüssels.
	log.Reset()
	if rotated, err := s.VerifyAndRotate(ctx); err != nil || rotated != 0 {
		t.Fatalf("second VerifyAndRotate = %d, %v; want 0, nil", rotated, err)
	}
	if !strings.Contains(log.String(), "MONIKA_MASTER_KEY_PREVIOUS can be removed") {
		t.Errorf("log = %q, want hint to remove the previous key", log.String())
	}

	// Ohne den passenden Schlüssel startet Monika nicht, und nichts wird verändert.
	foreign, _ := newTestService(t, store, Keys{Current: newTestKey(t), Previous: oldKey})
	snapshot := store.values[ids[0]]
	if _, err := foreign.VerifyAndRotate(ctx); err == nil || !strings.Contains(err.Error(), "2 of 2 secrets") {
		t.Fatalf("VerifyAndRotate with foreign key = %v, want error naming 2 of 2 secrets", err)
	}
	if !bytes.Equal(store.values[ids[0]].Ciphertext, snapshot.Ciphertext) {
		t.Fatal("secret changed although verification failed")
	}
}

func TestVerifyAndRotateRefusesTamperedValue(t *testing.T) {
	ctx := t.Context()
	key := newTestKey(t)
	store := newMemStore()
	s, _ := newTestService(t, store, Keys{Current: key})
	sec, err := s.Create(ctx, uuid.Nil, "a", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	v := store.values[sec.ID]
	v.Ciphertext[0] ^= 1
	store.values[sec.ID] = v

	if _, err := s.VerifyAndRotate(ctx); err == nil {
		t.Fatal("VerifyAndRotate accepted a tampered secret")
	}
}
