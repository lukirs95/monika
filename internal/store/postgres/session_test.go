//go:build integration

package postgres_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestSessionsAndTokens(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	admin, anna := newUser(t, pool), newUser(t, pool)
	wall := auth.User{ID: newID(), Username: "wand-regie-2", Service: true}
	if err := s.CreateUser(ctx, admin, wall); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	token := auth.Token{ID: newID(), UserID: wall.ID, Name: "Wand", ReadOnly: true}
	if err := s.CreateToken(ctx, admin, token, "hash-of-token"); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	gotTok, gotUser, err := s.TokenByHash(ctx, "hash-of-token")
	if err != nil || gotTok.ID != token.ID || !gotTok.ReadOnly || gotUser.ID != wall.ID {
		t.Fatalf("TokenByHash = %+v, %+v, %v", gotTok, gotUser, err)
	}
	if _, _, err := s.TokenByHash(ctx, "unknown"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("TokenByHash unknown = %v, want ErrNotFound", err)
	}

	expires := time.Now().Add(7 * 24 * time.Hour)
	userSession := auth.Session{ID: newID(), UserID: anna, Kind: auth.SessionUser, ExpiresAt: expires, UserAgent: "Firefox"}
	kiosk := auth.Session{ID: newID(), UserID: wall.ID, Kind: auth.SessionKiosk, TokenID: token.ID, ExpiresAt: expires}
	for _, sess := range []auth.Session{userSession, kiosk} {
		if err := s.CreateSession(ctx, sess, "hash-"+sess.ID.String()); err != nil {
			t.Fatalf("CreateSession %s: %v", sess.Kind, err)
		}
	}
	if err := s.CreateSession(ctx, auth.Session{ID: newID(), UserID: anna, Kind: auth.SessionKiosk, ExpiresAt: expires}, "x"); err == nil {
		t.Fatal("kiosk session without token accepted, want check violation")
	}

	t.Run("lookup", func(t *testing.T) {
		sess, user, tok, err := s.SessionByHash(ctx, "hash-"+kiosk.ID.String())
		if err != nil || sess.Kind != auth.SessionKiosk || user.ID != wall.ID || tok.ID != token.ID || !tok.ReadOnly {
			t.Fatalf("SessionByHash kiosk = %+v, %+v, %+v, %v", sess, user, tok, err)
		}
		sess, _, tok, err = s.SessionByHash(ctx, "hash-"+userSession.ID.String())
		if err != nil || sess.UserAgent != "Firefox" || tok.ID != uuid.Nil {
			t.Fatalf("SessionByHash user = %+v, %+v, %v; want no token", sess, tok, err)
		}
	})

	t.Run("touch only moves forward", func(t *testing.T) {
		later := time.Now().Add(time.Hour).Truncate(time.Microsecond)
		if err := s.TouchSessions(ctx, []auth.Touch{{ID: userSession.ID, At: later}}); err != nil {
			t.Fatalf("TouchSessions: %v", err)
		}
		if err := s.TouchSessions(ctx, []auth.Touch{{ID: userSession.ID, At: later.Add(-time.Minute)}}); err != nil {
			t.Fatalf("TouchSessions older: %v", err)
		}
		sessions, err := s.ListSessions(ctx, anna)
		if err != nil || len(sessions) != 1 || !sessions[0].LastSeenAt.Equal(later) {
			t.Fatalf("ListSessions = %+v, %v; want last_seen_at %v", sessions, err, later)
		}
	})

	t.Run("revoke", func(t *testing.T) {
		if err := s.RevokeSession(ctx, admin, userSession.ID); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("RevokeSession of another user = %v, want ErrNotFound", err)
		}
		if err := s.RevokeSession(ctx, anna, userSession.ID); err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
		if sessions, _ := s.ListSessions(ctx, anna); len(sessions) != 0 {
			t.Fatalf("sessions after revoke = %+v, want none", sessions)
		}
		if err := s.RevokeToken(ctx, admin, token.ID); err != nil {
			t.Fatalf("RevokeToken: %v", err)
		}
		if !sessionRevoked(t, pool, kiosk.ID) {
			t.Fatal("kiosk session not revoked with its token")
		}
		if err := s.RevokeToken(ctx, admin, token.ID); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("RevokeToken again = %v, want ErrNotFound", err)
		}
	})

	for _, e := range auditLog(t, pool) {
		if text := string(e.Before) + string(e.After); strings.Contains(text, "hash-of-token") || strings.Contains(text, "token_hash") {
			t.Errorf("audit entry %s %s contains the token hash", e.Verb, e.ObjectType)
		}
	}
}
