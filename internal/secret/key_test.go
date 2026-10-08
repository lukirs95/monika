package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func newTestKey(t *testing.T) Key {
	t.Helper()
	b := make([]byte, 32)
	rand.Read(b)
	k, err := ParseKey(base64.StdEncoding.EncodeToString(b))
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	return k
}

func TestParseKey(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid", base64.StdEncoding.EncodeToString(make([]byte, 32)), false},
		{"not base64", "kein base64!", true},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 16)), true},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 33)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, err := ParseKey(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseKey err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && len(k.ID()) != 16 {
				t.Fatalf("ID = %q, want 16 hex characters", k.ID())
			}
		})
	}
}

func TestKeyID(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	a, _ := ParseKey(enc)
	b, _ := ParseKey(enc)
	if a.ID() != b.ID() {
		t.Fatalf("same key, different IDs %s and %s", a.ID(), b.ID())
	}
	if c := newTestKey(t); c.ID() == a.ID() {
		t.Fatalf("different keys share ID %s", a.ID())
	}
}

func TestKeyNeverPrinted(t *testing.T) {
	k := newTestKey(t)
	raw := base64.StdEncoding.EncodeToString(k.bytes[:])
	var logged strings.Builder
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("start", "key", k)
	for _, out := range []string{
		fmt.Sprint(k), fmt.Sprintf("%v %+v %#v %s", k, k, k, k), logged.String(),
	} {
		if strings.Contains(out, raw) || !strings.Contains(out, redacted) {
			t.Errorf("output %q leaks the key or is not redacted", out)
		}
	}
}

func TestSealOpen(t *testing.T) {
	k := newTestKey(t)
	id := uuid.Must(uuid.NewV7())
	plaintext := []byte(`{"username": "admin", "password": "geheim"}`)

	e, err := k.seal(id, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if e.KeyID != k.ID() || len(e.Nonce) != 12 || bytes.Contains(e.Ciphertext, []byte("geheim")) {
		t.Fatalf("encrypted = %+v, want key id, 12-byte nonce and no plaintext", e)
	}
	got, err := k.open(e)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("open = %q, %v; want %q", got, err, plaintext)
	}

	again, _ := k.seal(id, plaintext)
	if bytes.Equal(again.Nonce, e.Nonce) {
		t.Fatal("two encryptions share a nonce")
	}

	tampered := func(f func(*Encrypted)) Encrypted {
		c := e
		c.Ciphertext = bytes.Clone(e.Ciphertext)
		f(&c)
		return c
	}
	other := newTestKey(t)
	failures := map[string]struct {
		key Key
		e   Encrypted
	}{
		"other key":              {other, e},
		"other key, same key id": {other, tampered(func(c *Encrypted) { c.KeyID = other.ID() })},
		"moved to other secret":  {k, tampered(func(c *Encrypted) { c.ID = uuid.Must(uuid.NewV7()) })},
		"ciphertext changed":     {k, tampered(func(c *Encrypted) { c.Ciphertext[0] ^= 1 })},
		"nonce too short":        {k, tampered(func(c *Encrypted) { c.Nonce = c.Nonce[:8] })},
	}
	for name, f := range failures {
		if _, err := f.key.open(f.e); err == nil {
			t.Errorf("%s: open succeeded, want error", name)
		}
	}
}
