package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) LoadAuthz(ctx context.Context) (auth.Authz, error) {
	var a auth.Authz
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, s.pool, opts, func(tx pgx.Tx) error {
		q := db.New(tx)
		roles, err := q.ListRoles(ctx)
		if err != nil {
			return fmt.Errorf("list roles: %w", err)
		}
		grants, err := q.ListGrants(ctx)
		if err != nil {
			return fmt.Errorf("list grants: %w", err)
		}
		members, err := q.ListRoleMembers(ctx)
		if err != nil {
			return fmt.Errorf("list members: %w", err)
		}
		a = auth.Authz{
			Roles: mapSlice(roles, toRole),
			Grants: mapSlice(grants, func(g db.PermissionGrant) auth.RoleGrant {
				return auth.RoleGrant{RoleID: g.RoleID, Grant: auth.Grant{
					Permission: auth.Permission(g.Permission), ScopeElementID: value(g.ScopeElementID)}}
			}),
			Members: mapSlice(members, func(m db.RoleMember) auth.Membership {
				return auth.Membership{RoleID: m.RoleID, UserID: m.UserID, FromIdP: m.Origin == "idp"}
			}),
		}
		return nil
	})
	return a, err
}

func (s *Store) ListUsers(ctx context.Context) ([]auth.User, error) {
	rows, err := s.q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, toUser), nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	n, err := s.q.CountUsers(ctx)
	return int(n), err
}

func (s *Store) UserByName(ctx context.Context, username string) (auth.User, error) {
	u, err := s.q.GetUserByName(ctx, username)
	return userResult(u, err)
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (auth.User, error) {
	u, err := s.q.GetUserByID(ctx, id)
	return userResult(u, err)
}

func (s *Store) CreateUser(ctx context.Context, actor uuid.UUID, u auth.User) error {
	err := s.q.CreateUser(ctx, db.CreateUserParams{
		ActorID: nullable(actor), ID: u.ID, Username: u.Username, DisplayName: nullable(u.DisplayName),
		Email: nullable(u.Email), PasswordHash: nullable(u.PasswordHash), IsService: u.Service,
	})
	return nameTaken(err)
}

func (s *Store) UpdateUser(ctx context.Context, actor uuid.UUID, u auth.User) error {
	n, err := s.q.UpdateUser(ctx, db.UpdateUserParams{
		ActorID: nullable(actor), ID: u.ID, DisplayName: nullable(u.DisplayName),
		Email: nullable(u.Email), Disabled: u.Disabled,
	})
	return affected(n, err, auth.ErrNotFound)
}

func (s *Store) SetPasswordHash(ctx context.Context, actor uuid.UUID, user uuid.UUID, hash string, exceptSession uuid.UUID) error {
	n, err := s.q.SetPasswordHash(ctx, db.SetPasswordHashParams{
		ActorID: nullable(actor), ID: user, PasswordHash: hash, ExceptSession: nullable(exceptSession),
	})
	return affected(n, err, auth.ErrNotFound)
}

func (s *Store) RecordLogin(ctx context.Context, l auth.Login) error {
	return s.q.RecordLogin(ctx, db.RecordLoginParams{
		Success: l.Success, UserID: nullable(l.UserID), Username: l.Username,
		RemoteAddr: nullable(l.RemoteAddr), Reason: nullable(l.Reason),
	})
}

func (s *Store) CreateRole(ctx context.Context, actor uuid.UUID, r auth.Role) error {
	err := s.q.CreateRole(ctx, db.CreateRoleParams{
		ActorID: nullable(actor), ID: r.ID, Name: r.Name,
		Description: nullable(r.Description), ExternalRef: nullable(r.ExternalRef),
	})
	return nameTaken(err)
}

func (s *Store) UpdateRole(ctx context.Context, actor uuid.UUID, r auth.Role) error {
	n, err := s.q.UpdateRole(ctx, db.UpdateRoleParams{
		ActorID: nullable(actor), ID: r.ID, Name: r.Name,
		Description: nullable(r.Description), ExternalRef: nullable(r.ExternalRef),
	})
	return affected(n, nameTaken(err), auth.ErrNotFound)
}

func (s *Store) DeleteRole(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	n, err := s.q.DeleteRole(ctx, db.DeleteRoleParams{ActorID: nullable(actor), ID: id})
	return affected(n, err, auth.ErrNotFound)
}

func (s *Store) SetGrants(ctx context.Context, actor uuid.UUID, role uuid.UUID, gs []auth.Grant) error {
	return s.changeRole(ctx, actor, role, func(q *db.Queries) error {
		if err := q.DeleteGrants(ctx, role); err != nil {
			return err
		}
		params := mapSlice(gs, func(g auth.Grant) db.InsertGrantParams {
			return db.InsertGrantParams{RoleID: role, Permission: db.Permission(g.Permission), ScopeElementID: nullable(g.ScopeElementID)}
		})
		var err error
		q.InsertGrant(ctx, params).Exec(func(i int, e error) {
			if err == nil && e != nil {
				err = fmt.Errorf("insert grant %s: %w", params[i].Permission, e)
			}
		})
		return err
	})
}

func (s *Store) SetMembers(ctx context.Context, actor uuid.UUID, role uuid.UUID, users []uuid.UUID) error {
	return s.changeRole(ctx, actor, role, func(q *db.Queries) error {
		if err := q.DeleteManualMembersExcept(ctx, db.DeleteManualMembersExceptParams{RoleID: role, KeepUserIds: users}); err != nil {
			return err
		}
		params := mapSlice(users, func(u uuid.UUID) db.InsertManualMemberParams {
			return db.InsertManualMemberParams{RoleID: role, UserID: u}
		})
		var err error
		q.InsertManualMember(ctx, params).Exec(func(i int, e error) {
			if err == nil && e != nil {
				err = fmt.Errorf("insert member %s: %w", params[i].UserID, e)
			}
		})
		return err
	})
}

// changeRole führt eine Änderung an Rechten oder Mitgliedern einer Rolle in einer Transaktion aus
// und schreibt den Audit-Eintrag mit dem Stand der Rolle vorher und nachher.
func (s *Store) changeRole(ctx context.Context, actor, role uuid.UUID, change func(q *db.Queries) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		before, err := q.RoleSnapshot(ctx, role)
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := change(q); err != nil {
			return err
		}
		after, err := q.RoleSnapshot(ctx, role)
		if err != nil {
			return err
		}
		return q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
			ActorID: nullable(actor), Verb: "update", ObjectType: "role", ObjectID: role, Before: before, After: after,
		})
	})
}

func userResult(u db.AppUser, err error) (auth.User, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.User{}, err
	}
	return toUser(u), nil
}

// nameTaken übersetzt eine Verletzung eines eindeutigen Namens in auth.ErrNameTaken.
func nameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.UniqueViolation {
		return auth.ErrNameTaken
	}
	return err
}

func toUser(u db.AppUser) auth.User {
	return auth.User{
		ID: u.ID, Username: u.Username, DisplayName: value(u.DisplayName), Email: value(u.Email),
		PasswordHash: value(u.PasswordHash), Service: u.IsService, Disabled: u.Disabled,
		CreatedAt: u.CreatedAt, LastLoginAt: value(u.LastLoginAt),
	}
}

func toRole(r db.Role) auth.Role {
	return auth.Role{ID: r.ID, Name: r.Name, Description: value(r.Description), ExternalRef: value(r.ExternalRef)}
}
