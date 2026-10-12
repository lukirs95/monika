package auth

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/inventory"
)

func (m *memStore) LoadAuthz(context.Context) (Authz, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.authzData, nil
}

func (m *memStore) CreateInitialAdmin(_ context.Context, u User, r Role, gs []Grant) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.users) > 0 {
		return false, nil
	}
	m.users[u.ID] = u
	m.authzData.Roles = append(m.authzData.Roles, r)
	for _, g := range gs {
		m.authzData.Grants = append(m.authzData.Grants, RoleGrant{RoleID: r.ID, Grant: g})
	}
	m.authzData.Members = append(m.authzData.Members, Membership{RoleID: r.ID, UserID: u.ID})
	return true, nil
}

func (m *memStore) CreateUser(_ context.Context, _ uuid.UUID, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[u.ID] = u
	return nil
}

func (m *memStore) SetGrants(_ context.Context, _ uuid.UUID, role uuid.UUID, gs []Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range gs {
		m.authzData.Grants = append(m.authzData.Grants, RoleGrant{RoleID: role, Grant: g})
	}
	return nil
}

// scopeView: Gerät X8 mit Zugang und Port, Gerät X9, Gruppe G mit X8.
func scopeView(t *testing.T) (v *core.View, x8, access, port, x9, g uuid.UUID) {
	t.Helper()
	x8, access, port, x9, g = uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	inv := inventory.New(nil, nil, slog.New(slog.DiscardHandler))
	inv.Load(inventory.Snapshot{
		Elements: []inventory.Element{
			{ID: x8, Kind: inventory.KindDevice, DeviceID: x8},
			{ID: access, Kind: inventory.KindAccess, ParentID: x8, DeviceID: x8},
			{ID: port, Kind: "port", ParentID: access, DeviceID: x8},
			{ID: x9, Kind: inventory.KindDevice, DeviceID: x9},
			{ID: g, Kind: inventory.KindGroup},
		},
		Links: []inventory.Link{{ID: uuid.New(), Type: inventory.LinkMember, FromID: g, ToID: x8}},
	})
	return &core.View{Graph: inv.Graph()}, x8, access, port, x9, g
}

func TestAllowed(t *testing.T) {
	v, x8, access, port, x9, g := scopeView(t)
	store := newMemStore()
	anna, bob := uuid.New(), uuid.New()
	ops, viewers, presets := uuid.New(), uuid.New(), uuid.New()
	store.authzData = Authz{
		Grants: []RoleGrant{
			{RoleID: ops, Grant: Grant{Permission: PermOperate, ScopeElementID: g}},
			{RoleID: viewers, Grant: Grant{Permission: PermView, ScopeElementID: access}},
			{RoleID: presets, Grant: Grant{Permission: PermPresetRun}},
			{RoleID: presets, Grant: Grant{Permission: PermCritical, ScopeElementID: port}},
		},
		Members: []Membership{{RoleID: ops, UserID: anna}, {RoleID: viewers, UserID: anna}, {RoleID: presets, UserID: anna},
			{RoleID: viewers, UserID: bob}},
	}
	s := New(Config{Store: store, Log: slog.New(slog.DiscardHandler)})
	if err := s.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	a, b := Principal{UserID: anna}, Principal{UserID: bob}
	tests := []struct {
		name    string
		p       Principal
		perm    Permission
		element uuid.UUID
		want    bool
	}{
		{"group grant covers device", a, PermOperate, x8, true},
		{"group grant covers descendants", a, PermOperate, port, true},
		{"not in the group", a, PermOperate, x9, false},
		{"view at the access element", a, PermView, port, true},
		{"scaffold: ancestor not covered", a, PermView, x8, false},
		{"rights do not imply each other", a, PermView, x9, false},
		{"critical only where granted", a, PermCritical, port, true},
		{"critical elsewhere", a, PermCritical, access, false},
		{"global right", a, PermPresetRun, uuid.Nil, true},
		{"scoped right asked globally", a, PermOperate, uuid.Nil, false},
		{"other user", b, PermOperate, port, false},
		{"other user's own role", b, PermView, port, true},
		{"unknown user", Principal{UserID: uuid.New()}, PermView, port, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.Allowed(v, tt.p, tt.perm, tt.element); got != tt.want {
				t.Errorf("Allowed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdministration(t *testing.T) {
	store := newMemStore()
	s := New(Config{Store: store, Log: slog.New(slog.DiscardHandler)})
	ctx := t.Context()

	created, err := s.EnsureAdmin(ctx, "Admin", "correct horse battery")
	if err != nil || !created {
		t.Fatalf("EnsureAdmin = %v, %v", created, err)
	}
	var admin User
	for _, u := range store.users {
		admin = u
	}
	v, _, _, port, _, _ := scopeView(t)
	for _, p := range Permissions {
		if !s.Allowed(v, Principal{UserID: admin.ID}, p, port) {
			t.Errorf("admin lacks %s", p)
		}
	}
	if admin.Username != "admin" || !VerifyPassword("correct horse battery", admin.PasswordHash) {
		t.Errorf("admin = %+v", admin)
	}
	if again, _ := s.EnsureAdmin(ctx, "other", "correct horse battery"); again {
		t.Error("second admin created")
	}
	if _, err := New(Config{Store: newMemStore()}).EnsureAdmin(ctx, "admin", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak admin password = %v", err)
	}

	users := []struct {
		name string
		u    NewUser
		want error
	}{
		{"service account", NewUser{ID: uuid.New(), Username: "wand-1", Service: true}, nil},
		{"human", NewUser{ID: uuid.New(), Username: "anna", Password: "correct horse battery"}, nil},
		{"service with password", NewUser{ID: uuid.New(), Username: "x", Service: true, Password: "correct horse battery"}, ErrInvalidUser},
		{"bad name", NewUser{ID: uuid.New(), Username: "anna müller", Password: "correct horse battery"}, ErrInvalidUser},
		{"weak password", NewUser{ID: uuid.New(), Username: "bob", Password: "short"}, ErrWeakPassword},
	}
	for _, tt := range users {
		if err := s.CreateUser(ctx, admin.ID, tt.u); !errors.Is(err, tt.want) {
			t.Errorf("%s: CreateUser = %v, want %v", tt.name, err, tt.want)
		}
	}

	grants := []struct {
		name string
		g    Grant
		ok   bool
	}{
		{"scoped view", Grant{Permission: PermView, ScopeElementID: uuid.New()}, true},
		{"global preset_run", Grant{Permission: PermPresetRun}, true},
		{"scoped preset_run", Grant{Permission: PermPresetRun, ScopeElementID: uuid.New()}, false},
		{"unknown", Grant{Permission: "superuser"}, false},
	}
	for _, tt := range grants {
		version := s.AuthzVersion()
		err := s.SetGrants(ctx, admin.ID, uuid.New(), []Grant{tt.g})
		if (err == nil) != tt.ok || (err != nil && !errors.Is(err, ErrInvalidGrant)) {
			t.Errorf("%s: SetGrants = %v", tt.name, err)
		}
		// Nur eine gelungene Änderung zählt; offene Ströme laden dann neu.
		if changed := s.AuthzVersion() != version; changed != tt.ok {
			t.Errorf("%s: authz version changed = %v", tt.name, changed)
		}
	}
}
