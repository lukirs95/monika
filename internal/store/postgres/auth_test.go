//go:build integration

package postgres_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
)

const fakeHash = "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"

// newSession legt eine Sitzung direkt an (Sitzungen bekommen ihren Store in einem eigenen Schritt).
func newSession(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) uuid.UUID {
	t.Helper()
	id := newID()
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO user_session (id, user_id, kind, token_hash, expires_at) VALUES ($1, $2, 'user', $3, now() + interval '7 days')`,
		id, user, id.String()); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return id
}

func sessionRevoked(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var revoked bool
	if err := pool.QueryRow(t.Context(), `SELECT revoked_at IS NOT NULL FROM user_session WHERE id = $1`, id).Scan(&revoked); err != nil {
		t.Fatalf("query session: %v", err)
	}
	return revoked
}

func TestUsers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	admin := newUser(t, pool)

	u := auth.User{ID: newID(), Username: "anna", DisplayName: "Anna", PasswordHash: fakeHash}
	if err := s.CreateUser(ctx, admin, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := s.CreateUser(ctx, admin, auth.User{ID: newID(), Username: "anna"}); !errors.Is(err, auth.ErrNameTaken) {
		t.Fatalf("CreateUser duplicate = %v, want ErrNameTaken", err)
	}
	got, err := s.UserByName(ctx, "anna")
	if err != nil || got.ID != u.ID || got.PasswordHash != fakeHash || got.DisplayName != "Anna" {
		t.Fatalf("UserByName = %+v, %v", got, err)
	}
	if _, err := s.UserByName(ctx, "niemand"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("UserByName unknown = %v, want ErrNotFound", err)
	}

	t.Run("password change keeps the current session", func(t *testing.T) {
		current, other := newSession(t, pool, u.ID), newSession(t, pool, u.ID)
		if err := s.SetPasswordHash(ctx, u.ID, u.ID, fakeHash+"neu", current); err != nil {
			t.Fatalf("SetPasswordHash: %v", err)
		}
		if sessionRevoked(t, pool, current) || !sessionRevoked(t, pool, other) {
			t.Fatal("want current session kept, other revoked")
		}
	})

	t.Run("disabling ends all sessions", func(t *testing.T) {
		sess := newSession(t, pool, u.ID)
		disabled := u
		disabled.Disabled = true
		if err := s.UpdateUser(ctx, admin, disabled); err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}
		if !sessionRevoked(t, pool, sess) {
			t.Fatal("session not revoked after disabling")
		}
	})

	t.Run("logins", func(t *testing.T) {
		for _, l := range []auth.Login{
			{UserID: u.ID, Username: "anna", RemoteAddr: "10.0.0.7", Success: true},
			{Username: "niemand", RemoteAddr: "10.0.0.7", Reason: "unknown_user"},
		} {
			if err := s.RecordLogin(ctx, l); err != nil {
				t.Fatalf("RecordLogin: %v", err)
			}
		}
		if got, _ := s.UserByID(ctx, u.ID); got.LastLoginAt.IsZero() {
			t.Error("last_login_at not set")
		}
	})

	var verbs []string
	for _, e := range auditLog(t, pool) {
		verbs = append(verbs, e.Verb)
		if text := string(e.Before) + string(e.After); strings.Contains(text, "argon2id") || strings.Contains(text, "password_hash") {
			t.Errorf("audit entry %s contains the password hash: %s", e.Verb, text)
		}
	}
	if want := []string{"create", "update", "update", "login", "login_failed"}; !slices.Equal(verbs, want) {
		t.Errorf("audit verbs = %v, want %v", verbs, want)
	}
}

func TestRolesAndGrants(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	admin := newUser(t, pool)
	anna, ben := newUser(t, pool), newUser(t, pool)

	device := newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	role := auth.Role{ID: newID(), Name: "Operator"}
	if err := s.CreateRole(ctx, admin, role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if err := s.CreateRole(ctx, admin, auth.Role{ID: newID(), Name: "Operator"}); !errors.Is(err, auth.ErrNameTaken) {
		t.Fatalf("CreateRole duplicate = %v, want ErrNameTaken", err)
	}
	grants := []auth.Grant{{Permission: auth.PermView}, {Permission: auth.PermOperate, ScopeElementID: device}}
	if err := s.SetGrants(ctx, admin, role.ID, grants); err != nil {
		t.Fatalf("SetGrants: %v", err)
	}
	for _, p := range []auth.Permission{auth.PermAdmin, auth.PermSourceEdit} {
		if err := s.SetGrants(ctx, admin, role.ID, []auth.Grant{{Permission: p, ScopeElementID: device}}); err == nil {
			t.Fatalf("SetGrants with scoped %s succeeded, want check violation", p)
		}
	}

	// Ben kommt aus dem IdP und bleibt, wenn die manuellen Mitglieder ersetzt werden.
	if _, err := pool.Exec(ctx, `INSERT INTO role_member (role_id, user_id, origin) VALUES ($1, $2, 'idp')`, role.ID, ben); err != nil {
		t.Fatalf("insert idp member: %v", err)
	}
	if err := s.SetMembers(ctx, admin, role.ID, []uuid.UUID{anna}); err != nil {
		t.Fatalf("SetMembers: %v", err)
	}
	if err := s.SetMembers(ctx, admin, role.ID, nil); err != nil {
		t.Fatalf("SetMembers empty: %v", err)
	}

	a, err := s.LoadAuthz(ctx)
	if err != nil {
		t.Fatalf("LoadAuthz: %v", err)
	}
	if len(a.Roles) != 1 || len(a.Grants) != 2 {
		t.Fatalf("authz roles = %d, grants = %d; want 1, 2", len(a.Roles), len(a.Grants))
	}
	if len(a.Members) != 1 || a.Members[0].UserID != ben || !a.Members[0].FromIdP {
		t.Fatalf("members = %+v, want only ben from the IdP", a.Members)
	}

	if err := s.DeleteRole(ctx, admin, role.ID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}
	if a, _ := s.LoadAuthz(ctx); len(a.Roles)+len(a.Grants)+len(a.Members) != 0 {
		t.Fatalf("authz after delete = %+v, want empty", a)
	}
	if err := s.SetGrants(ctx, admin, role.ID, grants); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("SetGrants on deleted role = %v, want ErrNotFound", err)
	}
}
