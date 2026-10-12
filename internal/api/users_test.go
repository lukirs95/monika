package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
)

// fakeAdmin hält Benutzer, Tokens und Rollen im Speicher.
type fakeAdmin struct {
	mu        sync.Mutex
	users     map[uuid.UUID]auth.User
	tokens    map[uuid.UUID]auth.Token
	authz     auth.Authz
	passwords map[uuid.UUID]uuid.UUID // Benutzer → behaltene Sitzung beim letzten SetPassword
}

func newFakeAdmin() *fakeAdmin {
	return &fakeAdmin{users: map[uuid.UUID]auth.User{}, tokens: map[uuid.UUID]auth.Token{}, passwords: map[uuid.UUID]uuid.UUID{}}
}

func (f *fakeAdmin) ListUsers(context.Context) ([]auth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []auth.User
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeAdmin) CreateUser(_ context.Context, _ uuid.UUID, nu auth.NewUser) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !nu.Service && len(nu.Password) < 12 {
		return auth.ErrWeakPassword
	}
	f.users[nu.ID] = auth.User{ID: nu.ID, Username: nu.Username, DisplayName: nu.DisplayName, Service: nu.Service, PasswordHash: "argon2id$" + nu.Password, CreatedAt: time.Now()}
	return nil
}

func (f *fakeAdmin) UpdateUser(_ context.Context, _ uuid.UUID, u auth.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, ok := f.users[u.ID]
	if !ok {
		return auth.ErrNotFound
	}
	old.DisplayName, old.Email, old.Disabled = u.DisplayName, u.Email, u.Disabled
	f.users[u.ID] = old
	return nil
}

func (f *fakeAdmin) SetPassword(_ context.Context, _, user uuid.UUID, _ string, keep uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.passwords[user] = keep
	return nil
}

func (*fakeAdmin) ResetTOTP(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f *fakeAdmin) ListTokens(_ context.Context, user uuid.UUID) ([]auth.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []auth.Token
	for _, t := range f.tokens {
		if t.UserID == user {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeAdmin) CreateToken(_ context.Context, _, user uuid.UUID, name string, readOnly bool, expires time.Time) (auth.Token, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[user]; !ok {
		return auth.Token{}, "", auth.ErrNotFound
	}
	t := auth.Token{ID: newID(), UserID: user, Name: name, ReadOnly: readOnly, ExpiresAt: expires, CreatedAt: time.Now()}
	f.tokens[t.ID] = t
	return t, "monika_plaintext", nil
}

func (f *fakeAdmin) RevokeToken(_ context.Context, _, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.tokens[id]; !ok {
		return auth.ErrNotFound
	}
	delete(f.tokens, id)
	return nil
}

func (f *fakeAdmin) Authz() auth.Authz {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authz
}

func (f *fakeAdmin) CreateRole(_ context.Context, _ uuid.UUID, r auth.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authz.Roles = append(f.authz.Roles, r)
	return nil
}

func (f *fakeAdmin) UpdateRole(_ context.Context, _ uuid.UUID, r auth.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.authz.Roles {
		if f.authz.Roles[i].ID == r.ID {
			f.authz.Roles[i] = r
			return nil
		}
	}
	return auth.ErrNotFound
}

func (f *fakeAdmin) DeleteRole(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f *fakeAdmin) SetGrants(_ context.Context, _, role uuid.UUID, gs []auth.Grant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range gs {
		if g.Permission.Global() && g.ScopeElementID != uuid.Nil {
			return auth.ErrInvalidGrant
		}
		f.authz.Grants = append(f.authz.Grants, auth.RoleGrant{RoleID: role, Grant: g})
	}
	return nil
}

func (f *fakeAdmin) SetMembers(_ context.Context, _, role uuid.UUID, users []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range users {
		f.authz.Members = append(f.authz.Members, auth.Membership{RoleID: role, UserID: u})
	}
	return nil
}

func TestUsersTokensRoles(t *testing.T) {
	s := newTestServer(t)
	admin := newFakeAdmin()
	s.cfg.Admin = admin
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{boss.UserID: {{Permission: auth.PermAdmin}}, viewer.UserID: {{Permission: auth.PermView}}}

	b := run(t, s, []step{
		{"admin only", "GET", "/api/v1/users", "", "viewer", 403, ""},
		{"create user", "POST", "/api/v1/users", `{"username":"anna","service":false,"password":"correct horse battery"}`, "boss", 201, `"username":"anna"`},
		{"weak password", "POST", "/api/v1/users", `{"username":"bob","service":false,"password":"kurz"}`, "boss", 422, `"code":"weak_password"`},
		{"service account", "POST", "/api/v1/users", `{"username":"wand","service":true}`, "boss", 201, `"service":true`},
		{"create role", "POST", "/api/v1/roles", `{"name":"Technik"}`, "boss", 201, `"grants":[]`},
	})
	for name, body := range b {
		if strings.Contains(string(body), "correct horse") || strings.Contains(string(body), "argon2id") {
			t.Errorf("%s returned the password: %s", name, body)
		}
	}
	annaID, wand, role := idOf(t, b["create user"]), idOf(t, b["service account"]), idOf(t, b["create role"])
	scope := uuid.NewString()
	b = run(t, s, []step{
		{"update user", "PUT", "/api/v1/users/" + annaID, `{"display_name":"Anna","email":null,"disabled":true}`, "boss", 200, `"disabled":true`},
		{"update unknown", "PUT", "/api/v1/users/" + uuid.NewString(), `{"display_name":null,"email":null,"disabled":false}`, "boss", 404, ""},
		{"set password", "PUT", "/api/v1/users/" + annaID + "/password", `{"password":"another correct horse"}`, "boss", 204, ""},
		{"reset totp", "POST", "/api/v1/users/" + annaID + "/totp/reset", "", "boss", 204, ""},
		{"create token", "POST", "/api/v1/users/" + wand + "/tokens", `{"name":"Wand Regie","read_only":true}`, "boss", 201, `"token":"monika_plaintext"`},
		{"token for unknown user", "POST", "/api/v1/users/" + uuid.NewString() + "/tokens", `{"name":"x","read_only":true}`, "boss", 404, ""},
		{"list tokens", "GET", "/api/v1/users/" + wand + "/tokens", "", "boss", 200, `"name":"Wand Regie"`},
		{"grants", "PUT", "/api/v1/roles/" + role + "/grants", `[{"permission":"view","scope_element_id":"` + scope + `"},{"permission":"operate"}]`, "boss", 204, ""},
		{"global permission with scope", "PUT", "/api/v1/roles/" + role + "/grants", `[{"permission":"admin","scope_element_id":"` + scope + `"}]`, "boss", 422, `"code":"global_permission"`},
		{"members", "PUT", "/api/v1/roles/" + role + "/members", `["` + annaID + `"]`, "boss", 204, ""},
		{"unknown role", "PUT", "/api/v1/roles/" + uuid.NewString() + "/members", `[]`, "boss", 404, ""},
		{"list roles", "GET", "/api/v1/roles", "", "boss", 200, `"members":["` + annaID + `"]`},
		{"user with role", "GET", "/api/v1/users", "", "boss", 200, `"roles":["` + role + `"]`},
	})
	if strings.Contains(string(b["list tokens"]), "monika_plaintext") {
		t.Error("token plaintext listed")
	}
	if keep, ok := admin.passwords[uuid.MustParse(annaID)]; !ok || keep != uuid.Nil {
		t.Errorf("password of another user keeps session %v", keep)
	}

	// Das eigene Passwort über die Verwaltung behält die eigene Sitzung.
	admin.users[boss.UserID] = auth.User{ID: boss.UserID, Username: "chefin"}
	run(t, s, []step{{"own password", "PUT", "/api/v1/users/" + boss.UserID.String() + "/password", `{"password":"another correct horse"}`, "boss", 204, ""}})
	if keep := admin.passwords[boss.UserID]; keep != boss.SessionID {
		t.Errorf("own password keeps session %v, want %v", keep, boss.SessionID)
	}
}
