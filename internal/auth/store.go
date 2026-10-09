package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	// ErrNotFound bedeutet, dass Benutzer, Rolle, Sitzung oder Token nicht existieren.
	ErrNotFound = errors.New("not found")
	// ErrNameTaken bedeutet, dass Benutzername bzw. Rollenname schon vergeben sind.
	ErrNameTaken = errors.New("name already taken")
)

// Store persistiert Benutzer, Rollen, Rechte, Sitzungen, Tokens und TOTP. Jede Methode ist eine in
// sich atomare Operation. Methoden, die der User auslöst, bekommen den handelnden Benutzer als
// actor und schreiben einen Eintrag ins Audit (nie Passwort-Hashes, Tokens oder Geheimnisse).
type Store interface {
	// LoadAuthz lädt Rollen, Rechte und Mitgliedschaften als konsistenten Stand.
	LoadAuthz(ctx context.Context) (Authz, error)
	ListUsers(ctx context.Context) ([]User, error)
	CountUsers(ctx context.Context) (int, error)
	// UserByName sucht nach dem kleingeschriebenen Namen. Unbekannt: ErrNotFound.
	UserByName(ctx context.Context, username string) (User, error)
	UserByID(ctx context.Context, id uuid.UUID) (User, error)
	// CreateUser legt einen Benutzer an. Name vergeben: ErrNameTaken.
	CreateUser(ctx context.Context, actor uuid.UUID, u User) error
	// UpdateUser ändert Anzeigename, E-Mail und Deaktivierung; Deaktivieren beendet alle Sitzungen.
	UpdateUser(ctx context.Context, actor uuid.UUID, u User) error
	// SetPasswordHash setzt den Hash und beendet alle Sitzungen des Benutzers außer exceptSession.
	SetPasswordHash(ctx context.Context, actor uuid.UUID, user uuid.UUID, hash string, exceptSession uuid.UUID) error
	// RecordLogin schreibt login bzw. login_failed ins Audit, bei Erfolg auch last_login_at.
	RecordLogin(ctx context.Context, l Login) error

	CreateRole(ctx context.Context, actor uuid.UUID, r Role) error
	UpdateRole(ctx context.Context, actor uuid.UUID, r Role) error
	// DeleteRole löscht die Rolle samt Rechten und Mitgliedschaften.
	DeleteRole(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
	// SetGrants ersetzt die Rechte einer Rolle.
	SetGrants(ctx context.Context, actor uuid.UUID, role uuid.UUID, gs []Grant) error
	// SetMembers ersetzt die manuellen Mitglieder einer Rolle; Mitgliedschaften aus dem IdP bleiben.
	SetMembers(ctx context.Context, actor uuid.UUID, role uuid.UUID, users []uuid.UUID) error
}
