package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/secret"
)

var (
	// ErrNotFound bedeutet, dass Benutzer, Rolle, Sitzung oder Token nicht existieren.
	ErrNotFound = errors.New("not found")
	// ErrNameTaken bedeutet, dass Benutzername bzw. Rollenname schon vergeben sind.
	ErrNameTaken = errors.New("name already taken")
	// ErrTOTPConfirmed bedeutet, dass bereits ein bestätigtes TOTP existiert; es lässt sich nur
	// zurücksetzen, nicht überschreiben.
	ErrTOTPConfirmed = errors.New("totp already confirmed")
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

	// CreateInitialAdmin legt in einer Transaktion Benutzer, Rolle, Rechte und Mitgliedschaft an,
	// aber nur, wenn es noch keinen Benutzer gibt; sonst false (M8).
	CreateInitialAdmin(ctx context.Context, u User, r Role, gs []Grant) (bool, error)

	CreateRole(ctx context.Context, actor uuid.UUID, r Role) error
	UpdateRole(ctx context.Context, actor uuid.UUID, r Role) error
	// DeleteRole löscht die Rolle samt Rechten und Mitgliedschaften.
	DeleteRole(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
	// SetGrants ersetzt die Rechte einer Rolle.
	SetGrants(ctx context.Context, actor uuid.UUID, role uuid.UUID, gs []Grant) error
	// SetMembers ersetzt die manuellen Mitglieder einer Rolle; Mitgliedschaften aus dem IdP bleiben.
	SetMembers(ctx context.Context, actor uuid.UUID, role uuid.UUID, users []uuid.UUID) error

	CreateSession(ctx context.Context, s Session, tokenHash string) error
	// SessionByHash liefert Sitzung und Benutzer, bei Kiosk-Sitzungen auch den Stand des Tokens
	// (ID, ReadOnly, ExpiresAt, RevokedAt). Ob sie gilt, prüft der Aufrufer. Unbekannt: ErrNotFound.
	SessionByHash(ctx context.Context, tokenHash string) (Session, User, Token, error)
	// ListSessions liefert die aktiven Sitzungen eines Benutzers, neueste zuerst.
	ListSessions(ctx context.Context, user uuid.UUID) ([]Session, error)
	// TouchSessions aktualisiert last_seen_at gesammelt; ältere Zeitpunkte werden ignoriert.
	TouchSessions(ctx context.Context, ts []Touch) error
	// RevokeSession beendet eine Sitzung des Benutzers. Unbekannt oder schon beendet: ErrNotFound.
	RevokeSession(ctx context.Context, user uuid.UUID, id uuid.UUID) error

	// CreateToken legt ein Token an; gespeichert und protokolliert wird nie der Klartext.
	CreateToken(ctx context.Context, actor uuid.UUID, t Token, tokenHash string) error
	// TokenByHash liefert Token und Benutzer. Ob es gilt, prüft der Aufrufer. Unbekannt: ErrNotFound.
	TokenByHash(ctx context.Context, tokenHash string) (Token, User, error)
	ListTokens(ctx context.Context, user uuid.UUID) ([]Token, error)
	TouchTokens(ctx context.Context, ts []Touch) error
	// RevokeToken widerruft ein Token und beendet seine Kiosk-Sitzungen. Unbekannt: ErrNotFound.
	RevokeToken(ctx context.Context, actor uuid.UUID, id uuid.UUID) error

	// TOTP liefert das TOTP eines Benutzers. Keines: ErrNotFound.
	TOTP(ctx context.Context, user uuid.UUID) (TOTP, error)
	// SaveTOTP speichert ein neues, unbestätigtes Geheimnis. Schon bestätigt: ErrTOTPConfirmed.
	SaveTOTP(ctx context.Context, user uuid.UUID, v secret.Encrypted) error
	// ConfirmTOTP bestätigt die Einrichtung mit dem verwendeten Zeitschritt und ersetzt die
	// Wiederherstellungscodes (als Hash). Keine offene Einrichtung: ErrNotFound.
	ConfirmTOTP(ctx context.Context, user uuid.UUID, step int64, recoveryCodeHashes []string) error
	// UseTOTPStep verbraucht einen Zeitschritt; false, wenn er nicht neuer ist als der letzte.
	UseTOTPStep(ctx context.Context, user uuid.UUID, step int64) (bool, error)
	// UseRecoveryCode verbraucht einen Wiederherstellungscode; false, wenn unbekannt oder verbraucht.
	UseRecoveryCode(ctx context.Context, user uuid.UUID, codeHash string) (bool, error)
	// ResetTOTP löscht TOTP und Wiederherstellungscodes (Admin); bei der nächsten Anmeldung wird
	// neu eingerichtet. Keines: ErrNotFound.
	ResetTOTP(ctx context.Context, actor uuid.UUID, user uuid.UUID) error
}
