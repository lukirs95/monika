//go:build integration

package postgres_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestTOTP(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	admin, anna := newUser(t, pool), newUser(t, pool)

	if err := s.SaveTOTP(ctx, anna, encrypted(anna, "seed-1", "k1")); err != nil {
		t.Fatalf("SaveTOTP: %v", err)
	}
	// Eine offene Einrichtung darf ersetzt werden (z. B. QR-Code neu erzeugt).
	if err := s.SaveTOTP(ctx, anna, encrypted(anna, "seed-2", "k1")); err != nil {
		t.Fatalf("SaveTOTP again: %v", err)
	}
	got, err := s.TOTP(ctx, anna)
	if err != nil || string(got.Value.Ciphertext) != "seed-2" || !got.ConfirmedAt.IsZero() || got.Value.ID != anna {
		t.Fatalf("TOTP = %+v, %v; want unconfirmed seed-2", got, err)
	}

	codes := make([]string, 10)
	for i := range codes {
		codes[i] = fmt.Sprintf("code-hash-%d", i)
	}
	if err := s.ConfirmTOTP(ctx, anna, 100, codes); err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}
	if err := s.SaveTOTP(ctx, anna, encrypted(anna, "seed-3", "k1")); !errors.Is(err, auth.ErrTOTPConfirmed) {
		t.Fatalf("SaveTOTP after confirm = %v, want ErrTOTPConfirmed", err)
	}
	if err := s.ConfirmTOTP(ctx, anna, 101, codes); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("ConfirmTOTP again = %v, want ErrNotFound", err)
	}

	t.Run("codes are valid once", func(t *testing.T) {
		steps := []struct {
			step int64
			want bool
		}{{100, false}, {101, true}, {101, false}, {99, false}, {102, true}}
		for _, st := range steps {
			if ok, err := s.UseTOTPStep(ctx, anna, st.step); err != nil || ok != st.want {
				t.Errorf("UseTOTPStep(%d) = %v, %v; want %v", st.step, ok, err, st.want)
			}
		}
		recovery := []struct {
			hash string
			want bool
		}{{"code-hash-3", true}, {"code-hash-3", false}, {"unknown", false}}
		for _, r := range recovery {
			if ok, err := s.UseRecoveryCode(ctx, anna, r.hash); err != nil || ok != r.want {
				t.Errorf("UseRecoveryCode(%s) = %v, %v; want %v", r.hash, ok, err, r.want)
			}
		}
	})

	t.Run("reset", func(t *testing.T) {
		if err := s.ResetTOTP(ctx, admin, anna); err != nil {
			t.Fatalf("ResetTOTP: %v", err)
		}
		if _, err := s.TOTP(ctx, anna); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("TOTP after reset = %v, want ErrNotFound", err)
		}
		if ok, _ := s.UseRecoveryCode(ctx, anna, "code-hash-4"); ok {
			t.Fatal("recovery code still valid after reset")
		}
		if err := s.ResetTOTP(ctx, admin, anna); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("ResetTOTP again = %v, want ErrNotFound", err)
		}
	})

	var after []string
	for _, e := range auditLog(t, pool) {
		after = append(after, string(e.After))
	}
	if fmt.Sprint(after) != `[{"totp": "enabled"} {"totp": "reset"}]` {
		t.Errorf("audit after = %v, want enabled and reset only", after)
	}
}
