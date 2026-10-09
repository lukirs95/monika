package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) CreateSession(ctx context.Context, sess auth.Session, tokenHash string) error {
	return s.q.CreateSession(ctx, db.CreateSessionParams{
		ID: sess.ID, UserID: sess.UserID, Kind: db.SessionKind(sess.Kind), TokenID: nullable(sess.TokenID),
		TokenHash: tokenHash, ExpiresAt: sess.ExpiresAt,
		UserAgent: nullable(sess.UserAgent), RemoteAddr: nullable(sess.RemoteAddr),
	})
}

func (s *Store) SessionByHash(ctx context.Context, tokenHash string) (auth.Session, auth.User, auth.Token, error) {
	r, err := s.q.GetSessionByHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Session{}, auth.User{}, auth.Token{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Session{}, auth.User{}, auth.Token{}, err
	}
	sess := toSession(r.UserSession)
	var tok auth.Token
	if r.UserSession.TokenID != nil {
		tok = auth.Token{
			ID: *r.UserSession.TokenID, UserID: sess.UserID, ReadOnly: value(r.TokenReadOnly),
			ExpiresAt: value(r.TokenExpiresAt), RevokedAt: value(r.TokenRevokedAt),
		}
	}
	return sess, toUser(r.AppUser), tok, nil
}

func (s *Store) ListSessions(ctx context.Context, user uuid.UUID) ([]auth.Session, error) {
	rows, err := s.q.ListActiveSessions(ctx, user)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, toSession), nil
}

func (s *Store) TouchSessions(ctx context.Context, ts []auth.Touch) error {
	if len(ts) == 0 {
		return nil
	}
	var err error
	s.q.TouchSession(ctx, mapSlice(ts, func(t auth.Touch) db.TouchSessionParams {
		return db.TouchSessionParams{ID: t.ID, At: t.At}
	})).Exec(func(i int, e error) {
		if err == nil && e != nil {
			err = fmt.Errorf("touch session %s: %w", ts[i].ID, e)
		}
	})
	return err
}

func (s *Store) RevokeSession(ctx context.Context, user uuid.UUID, id uuid.UUID) error {
	n, err := s.q.RevokeSession(ctx, db.RevokeSessionParams{ID: id, UserID: user})
	return affected(n, err, auth.ErrNotFound)
}

func (s *Store) CreateToken(ctx context.Context, actor uuid.UUID, t auth.Token, tokenHash string) error {
	return s.q.CreateToken(ctx, db.CreateTokenParams{
		ActorID: nullable(actor), ID: t.ID, UserID: t.UserID, Name: t.Name, TokenHash: tokenHash,
		ReadOnly: t.ReadOnly, ExpiresAt: nullable(t.ExpiresAt),
	})
}

func (s *Store) TokenByHash(ctx context.Context, tokenHash string) (auth.Token, auth.User, error) {
	r, err := s.q.GetTokenByHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Token{}, auth.User{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Token{}, auth.User{}, err
	}
	return toToken(r.ApiToken), toUser(r.AppUser), nil
}

func (s *Store) ListTokens(ctx context.Context, user uuid.UUID) ([]auth.Token, error) {
	rows, err := s.q.ListTokens(ctx, user)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, toToken), nil
}

func (s *Store) TouchTokens(ctx context.Context, ts []auth.Touch) error {
	if len(ts) == 0 {
		return nil
	}
	var err error
	s.q.TouchToken(ctx, mapSlice(ts, func(t auth.Touch) db.TouchTokenParams {
		return db.TouchTokenParams{ID: t.ID, At: &t.At}
	})).Exec(func(i int, e error) {
		if err == nil && e != nil {
			err = fmt.Errorf("touch token %s: %w", ts[i].ID, e)
		}
	})
	return err
}

func (s *Store) RevokeToken(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	n, err := s.q.RevokeToken(ctx, db.RevokeTokenParams{ActorID: nullable(actor), ID: id})
	return affected(n, err, auth.ErrNotFound)
}

func toSession(s db.UserSession) auth.Session {
	return auth.Session{
		ID: s.ID, UserID: s.UserID, Kind: auth.SessionKind(s.Kind), TokenID: value(s.TokenID),
		CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt, RevokedAt: value(s.RevokedAt),
		UserAgent: value(s.UserAgent), RemoteAddr: value(s.RemoteAddr),
	}
}

func toToken(t db.ApiToken) auth.Token {
	return auth.Token{
		ID: t.ID, UserID: t.UserID, Name: t.Name, ReadOnly: t.ReadOnly, ExpiresAt: value(t.ExpiresAt),
		RevokedAt: value(t.RevokedAt), LastUsedAt: value(t.LastUsedAt), CreatedAt: t.CreatedAt,
	}
}
