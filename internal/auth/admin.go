package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Fehler der Verwaltung; die API macht daraus 422.
var (
	ErrInvalidUser  = errors.New("invalid user")
	ErrInvalidRole  = errors.New("invalid role")
	ErrInvalidGrant = errors.New("invalid grant")
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// NewUser ist ein neuer Benutzer; Password nur bei Menschen, Dienstkonten melden sich mit Token an.
type NewUser struct {
	ID          uuid.UUID
	Username    string
	DisplayName string
	Email       string
	Password    string
	Service     bool
}

// CreateUser legt einen Benutzer an; die ID vergibt die API.
func (s *Service) CreateUser(ctx context.Context, actor uuid.UUID, nu NewUser) error {
	u, err := s.newUser(nu)
	if err != nil {
		return err
	}
	return s.cfg.Store.CreateUser(ctx, actor, u)
}

func (s *Service) newUser(nu NewUser) (User, error) {
	name := strings.ToLower(strings.TrimSpace(nu.Username))
	if nu.ID == uuid.Nil || !usernamePattern.MatchString(name) {
		return User{}, fmt.Errorf("%w: needs an ID and a name of a–z, 0–9, '.', '_', '-'", ErrInvalidUser)
	}
	u := User{ID: nu.ID, Username: name, DisplayName: nu.DisplayName, Email: nu.Email, Service: nu.Service}
	switch {
	case nu.Service && nu.Password != "":
		return User{}, fmt.Errorf("%w: service accounts have no password", ErrInvalidUser)
	case !nu.Service:
		hash, err := HashPassword(nu.Password)
		if err != nil {
			return User{}, err
		}
		u.PasswordHash = hash
	}
	return u, nil
}

// UpdateUser ändert Anzeigename, E-Mail und Deaktivierung; Deaktivieren beendet alle Sitzungen.
func (s *Service) UpdateUser(ctx context.Context, actor uuid.UUID, u User) error {
	return s.cfg.Store.UpdateUser(ctx, actor, u)
}

// SetPassword setzt ein neues Passwort und beendet alle Sitzungen des Benutzers außer keepSession
// (die eigene, wenn er es selbst ändert).
func (s *Service) SetPassword(ctx context.Context, actor, user uuid.UUID, password string, keepSession uuid.UUID) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.cfg.Store.SetPasswordHash(ctx, actor, user, hash, keepSession)
}

// ResetTOTP löscht TOTP und Wiederherstellungscodes; die nächste Anmeldung richtet neu ein.
func (s *Service) ResetTOTP(ctx context.Context, actor, user uuid.UUID) error {
	return s.cfg.Store.ResetTOTP(ctx, actor, user)
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) { return s.cfg.Store.ListUsers(ctx) }

// ChangePassword ändert das eigene Passwort nach Prüfung des aktuellen und beendet alle anderen
// eigenen Sitzungen. Ein falsches aktuelles Passwort zählt wie ein Fehlversuch bei der Anmeldung
// (ErrInvalidCredentials, ThrottledError); Dienstkonten haben kein Passwort.
func (s *Service) ChangePassword(ctx context.Context, p Principal, current, next string, meta Meta) error {
	if wait := s.throttle.wait(p.Username, meta.RemoteAddr, s.now()); wait > 0 {
		return ThrottledError{Wait: wait}
	}
	u, err := s.cfg.Store.UserByID(ctx, p.UserID)
	if err != nil {
		return err
	}
	if u.PasswordHash == "" || !VerifyPassword(current, u.PasswordHash) {
		return s.failed(ctx, u, meta, "wrong_password", ErrInvalidCredentials)
	}
	if err := s.SetPassword(ctx, u.ID, u.ID, next, p.SessionID); err != nil {
		return err
	}
	s.throttle.succeed(u.Username, meta.RemoteAddr)
	return nil
}

// ListSessions liefert die gültigen Sitzungen eines Benutzers mit der letzten Aktivität aus dem
// Speicher; durch Leerlauf abgelaufene fehlen.
func (s *Service) ListSessions(ctx context.Context, user uuid.UUID) ([]Session, error) {
	all, err := s.cfg.Store.ListSessions(ctx, user)
	if err != nil {
		return nil, err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := all[:0]
	for _, sess := range all {
		if t := s.seen[sess.ID]; t.After(sess.LastSeenAt) {
			sess.LastSeenAt = t
		}
		if sess.Kind == SessionUser && now.Sub(sess.LastSeenAt) > s.cfg.SessionIdle {
			continue
		}
		out = append(out, sess)
	}
	return out, nil
}

func (s *Service) RevokeSession(ctx context.Context, user, id uuid.UUID) error {
	return s.cfg.Store.RevokeSession(ctx, user, id)
}

// CreateRole legt eine Rolle an; die ID vergibt die API.
func (s *Service) CreateRole(ctx context.Context, actor uuid.UUID, r Role) error {
	if r.ID == uuid.Nil || strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("%w: needs an ID and a name", ErrInvalidRole)
	}
	return s.afterAuthz(ctx, s.cfg.Store.CreateRole(ctx, actor, r))
}

func (s *Service) UpdateRole(ctx context.Context, actor uuid.UUID, r Role) error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("%w: needs a name", ErrInvalidRole)
	}
	return s.afterAuthz(ctx, s.cfg.Store.UpdateRole(ctx, actor, r))
}

func (s *Service) DeleteRole(ctx context.Context, actor, id uuid.UUID) error {
	return s.afterAuthz(ctx, s.cfg.Store.DeleteRole(ctx, actor, id))
}

// SetGrants ersetzt die Rechte einer Rolle; globale Rechte nur ohne Geltungsbereich.
func (s *Service) SetGrants(ctx context.Context, actor, role uuid.UUID, gs []Grant) error {
	for _, g := range gs {
		switch {
		case !g.Permission.Valid():
			return fmt.Errorf("%w: unknown permission %q", ErrInvalidGrant, g.Permission)
		case g.Permission.Global() && g.ScopeElementID != uuid.Nil:
			return fmt.Errorf("%w: %s is only global", ErrInvalidGrant, g.Permission)
		}
	}
	return s.afterAuthz(ctx, s.cfg.Store.SetGrants(ctx, actor, role, gs))
}

// SetMembers ersetzt die manuellen Mitglieder einer Rolle.
func (s *Service) SetMembers(ctx context.Context, actor, role uuid.UUID, users []uuid.UUID) error {
	return s.afterAuthz(ctx, s.cfg.Store.SetMembers(ctx, actor, role, users))
}

// afterAuthz lädt nach einer gelungenen Änderung Rollen und Rechte neu.
func (s *Service) afterAuthz(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	return s.reloadAuthz(ctx)
}

// CreateToken legt ein API-Token an und liefert den Klartext; danach ist er nie wieder sichtbar.
func (s *Service) CreateToken(ctx context.Context, actor, user uuid.UUID, name string, readOnly bool, expires time.Time) (string, error) {
	plain, hash := newAPIToken()
	t := Token{ID: uuid.Must(uuid.NewV7()), UserID: user, Name: name, ReadOnly: readOnly, ExpiresAt: expires}
	if err := s.cfg.Store.CreateToken(ctx, actor, t, hash); err != nil {
		return "", err
	}
	return plain, nil
}

func (s *Service) ListTokens(ctx context.Context, user uuid.UUID) ([]Token, error) {
	return s.cfg.Store.ListTokens(ctx, user)
}

// RevokeToken widerruft ein Token und beendet seine Kiosk-Sitzungen.
func (s *Service) RevokeToken(ctx context.Context, actor, id uuid.UUID) error {
	return s.cfg.Store.RevokeToken(ctx, actor, id)
}

// EnsureAdmin legt den initialen Admin mit einer Rolle „Administratoren“ und allen Rechten global
// an, aber nur, wenn es noch keinen Benutzer gibt (M8). Das Passwort muss die Regeln erfüllen.
func (s *Service) EnsureAdmin(ctx context.Context, username, password string) (bool, error) {
	u, err := s.newUser(NewUser{ID: uuid.Must(uuid.NewV7()), Username: username, Password: password})
	if err != nil {
		return false, err
	}
	role := Role{ID: uuid.Must(uuid.NewV7()), Name: "Administratoren", Description: "alle Rechte, beim ersten Start angelegt"}
	grants := make([]Grant, 0, len(Permissions))
	for _, p := range Permissions {
		grants = append(grants, Grant{Permission: p})
	}
	created, err := s.cfg.Store.CreateInitialAdmin(ctx, u, role, grants)
	if err != nil || !created {
		return false, err
	}
	return true, s.reloadAuthz(ctx)
}
