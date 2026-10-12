package api

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
)

// Benutzer, Tokens, Rollen und Rechte (admin). Klartext von Passwörtern und Tokens geht nie
// zurück, außer dem neuen Token in der Antwort, die es erzeugt.

func (s *Server) ListUsers(ctx context.Context, _ oapi.ListUsersRequestObject) (oapi.ListUsersResponseObject, error) {
	if _, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	users, err := s.cfg.Admin.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	roles := userRoles(s.cfg.Admin.Authz())
	out := oapi.ListUsers200JSONResponse{}
	for _, u := range users {
		out = append(out, toUser(u, roles[u.ID]))
	}
	return out, nil
}

// user liest einen Benutzer nach dem Schreiben.
func (s *Server) user(ctx context.Context, id uuid.UUID) (oapi.User, error) {
	users, err := s.cfg.Admin.ListUsers(ctx)
	if err != nil {
		return oapi.User{}, err
	}
	for _, u := range users {
		if u.ID == id {
			return toUser(u, userRoles(s.cfg.Admin.Authz())[id]), nil
		}
	}
	return oapi.User{}, errNotFound
}

func (s *Server) CreateUser(ctx context.Context, rq oapi.CreateUserRequestObject) (oapi.CreateUserResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	b := rq.Body
	nu := auth.NewUser{ID: newID(), Username: b.Username, DisplayName: value(b.DisplayName), Email: value(b.Email),
		Password: value(b.Password), Service: b.Service}
	if err := s.cfg.Admin.CreateUser(ctx, p.UserID, nu); err != nil {
		return nil, err
	}
	u, err := s.user(ctx, nu.ID)
	return oapi.CreateUser201JSONResponse(u), err
}

func (s *Server) UpdateUser(ctx context.Context, rq oapi.UpdateUserRequestObject) (oapi.UpdateUserResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	b := rq.Body
	u := auth.User{ID: rq.UserID, DisplayName: value(b.DisplayName), Email: value(b.Email), Disabled: b.Disabled}
	if err := s.cfg.Admin.UpdateUser(ctx, p.UserID, u); err != nil {
		return nil, err
	}
	out, err := s.user(ctx, rq.UserID)
	return oapi.UpdateUser200JSONResponse(out), err
}

func (s *Server) SetUserPassword(ctx context.Context, rq oapi.SetUserPasswordRequestObject) (oapi.SetUserPasswordResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	keep := uuid.Nil // beendet alle Sitzungen des Benutzers, außer der eigenen, wenn es die eigenen sind
	if rq.UserID == p.UserID {
		keep = p.SessionID
	}
	if err := s.cfg.Admin.SetPassword(ctx, p.UserID, rq.UserID, rq.Body.Password, keep); err != nil {
		return nil, err
	}
	return oapi.SetUserPassword204Response{}, nil
}

func (s *Server) ResetUserTOTP(ctx context.Context, rq oapi.ResetUserTOTPRequestObject) (oapi.ResetUserTOTPResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Admin.ResetTOTP(ctx, p.UserID, rq.UserID); err != nil {
		return nil, err
	}
	return oapi.ResetUserTOTP204Response{}, nil
}

func (s *Server) ListTokens(ctx context.Context, rq oapi.ListTokensRequestObject) (oapi.ListTokensResponseObject, error) {
	if _, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if _, err := s.user(ctx, rq.UserID); err != nil {
		return nil, err
	}
	tokens, err := s.cfg.Admin.ListTokens(ctx, rq.UserID)
	if err != nil {
		return nil, err
	}
	out := oapi.ListTokens200JSONResponse{}
	for _, t := range tokens {
		out = append(out, toToken(t))
	}
	return out, nil
}

func (s *Server) CreateToken(ctx context.Context, rq oapi.CreateTokenRequestObject) (oapi.CreateTokenResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	b := rq.Body
	t, plain, err := s.cfg.Admin.CreateToken(ctx, p.UserID, rq.UserID, b.Name, b.ReadOnly, value(b.ExpiresAt))
	if err != nil {
		return nil, err
	}
	tok := toToken(t)
	return oapi.CreateToken201JSONResponse{
		ID: tok.ID, UserID: tok.UserID, Name: tok.Name, ReadOnly: tok.ReadOnly, ExpiresAt: tok.ExpiresAt,
		CreatedAt: tok.CreatedAt, Token: plain,
	}, nil
}

func (s *Server) RevokeToken(ctx context.Context, rq oapi.RevokeTokenRequestObject) (oapi.RevokeTokenResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Admin.RevokeToken(ctx, p.UserID, rq.TokenID); err != nil {
		return nil, err
	}
	return oapi.RevokeToken204Response{}, nil
}

func (s *Server) ListRoles(ctx context.Context, _ oapi.ListRolesRequestObject) (oapi.ListRolesResponseObject, error) {
	if _, err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	a := s.cfg.Admin.Authz()
	out := oapi.ListRoles200JSONResponse{}
	for _, r := range a.Roles {
		out = append(out, toRole(r, a))
	}
	return out, nil
}

// role liest eine Rolle nach dem Schreiben.
func (s *Server) role(id uuid.UUID) (oapi.Role, error) {
	a := s.cfg.Admin.Authz()
	for _, r := range a.Roles {
		if r.ID == id {
			return toRole(r, a), nil
		}
	}
	return oapi.Role{}, errNotFound
}

func (s *Server) CreateRole(ctx context.Context, rq oapi.CreateRoleRequestObject) (oapi.CreateRoleResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	r := fromRoleInput(newID(), *rq.Body)
	if err := s.cfg.Admin.CreateRole(ctx, p.UserID, r); err != nil {
		return nil, err
	}
	out, err := s.role(r.ID)
	return oapi.CreateRole201JSONResponse(out), err
}

func (s *Server) UpdateRole(ctx context.Context, rq oapi.UpdateRoleRequestObject) (oapi.UpdateRoleResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Admin.UpdateRole(ctx, p.UserID, fromRoleInput(rq.RoleID, *rq.Body)); err != nil {
		return nil, err
	}
	out, err := s.role(rq.RoleID)
	return oapi.UpdateRole200JSONResponse(out), err
}

func (s *Server) DeleteRole(ctx context.Context, rq oapi.DeleteRoleRequestObject) (oapi.DeleteRoleResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Admin.DeleteRole(ctx, p.UserID, rq.RoleID); err != nil {
		return nil, err
	}
	return oapi.DeleteRole204Response{}, nil
}

func (s *Server) SetRoleGrants(ctx context.Context, rq oapi.SetRoleGrantsRequestObject) (oapi.SetRoleGrantsResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.role(rq.RoleID); err != nil {
		return nil, err
	}
	gs := []auth.Grant{}
	for _, g := range *rq.Body {
		gs = append(gs, auth.Grant{Permission: auth.Permission(g.Permission), ScopeElementID: valueID(g.ScopeElementID)})
	}
	if err := s.cfg.Admin.SetGrants(ctx, p.UserID, rq.RoleID, gs); err != nil {
		return nil, err
	}
	return oapi.SetRoleGrants204Response{}, nil
}

func (s *Server) SetRoleMembers(ctx context.Context, rq oapi.SetRoleMembersRequestObject) (oapi.SetRoleMembersResponseObject, error) {
	p, err := s.requireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.role(rq.RoleID); err != nil {
		return nil, err
	}
	if err := s.cfg.Admin.SetMembers(ctx, p.UserID, rq.RoleID, nonNil(*rq.Body)); err != nil {
		return nil, err
	}
	return oapi.SetRoleMembers204Response{}, nil
}

// userRoles liefert pro Benutzer seine Rollen.
func userRoles(a auth.Authz) map[uuid.UUID][]uuid.UUID {
	out := map[uuid.UUID][]uuid.UUID{}
	for _, m := range a.Members {
		out[m.UserID] = append(out[m.UserID], m.RoleID)
	}
	return out
}

func toUser(u auth.User, roles []uuid.UUID) oapi.User {
	roles = slices.Clone(nonNil(roles))
	slices.SortFunc(roles, compareIDs)
	return oapi.User{
		ID: u.ID, Username: u.Username, DisplayName: optional(u.DisplayName), Email: optional(u.Email),
		Service: u.Service, Disabled: u.Disabled, TotpEnabled: u.TOTPEnabled, Roles: roles,
		CreatedAt: u.CreatedAt.UTC(), LastLoginAt: optionalTime(u.LastLoginAt),
	}
}

func toToken(t auth.Token) oapi.Token {
	return oapi.Token{
		ID: t.ID, UserID: t.UserID, Name: t.Name, ReadOnly: t.ReadOnly, ExpiresAt: optionalTime(t.ExpiresAt),
		RevokedAt: optionalTime(t.RevokedAt), LastUsedAt: optionalTime(t.LastUsedAt), CreatedAt: t.CreatedAt.UTC(),
	}
}

func fromRoleInput(id uuid.UUID, in oapi.RoleInput) auth.Role {
	return auth.Role{ID: id, Name: in.Name, Description: value(in.Description), ExternalRef: value(in.ExternalRef)}
}

func toRole(r auth.Role, a auth.Authz) oapi.Role {
	out := oapi.Role{
		ID: r.ID, Name: r.Name, Description: optional(r.Description), ExternalRef: optional(r.ExternalRef),
		Grants: []oapi.Grant{}, Members: []uuid.UUID{},
	}
	for _, g := range a.Grants {
		if g.RoleID == r.ID {
			out.Grants = append(out.Grants, oapi.Grant{Permission: oapi.GrantPermission(g.Permission), ScopeElementID: optionalID(g.ScopeElementID)})
		}
	}
	for _, m := range a.Members {
		if m.RoleID == r.ID {
			out.Members = append(out.Members, m.UserID)
		}
	}
	slices.SortFunc(out.Members, compareIDs)
	return out
}
