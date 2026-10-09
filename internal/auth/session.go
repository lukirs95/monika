package auth

import (
	"time"

	"github.com/google/uuid"
)

// SessionKind unterscheidet Sitzungen von Menschen und von Dashboards.
type SessionKind string

const (
	SessionUser  SessionKind = "user"  // Passwort und TOTP; Leerlauf-Timeout
	SessionKiosk SessionKind = "kiosk" // Token mit read_only; gilt so lange wie das Token
)

// Session ist eine Sitzung im Cookie; gespeichert wird nur der Hash des Tokens.
type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Kind       SessionKind
	TokenID    uuid.UUID // nur bei Kiosk
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	RevokedAt  time.Time
	UserAgent  string
	RemoteAddr string
}

// Token ist ein API-Token; gespeichert wird nur der Hash.
type Token struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	ReadOnly   bool
	ExpiresAt  time.Time // Null-Zeit = kein Ablauf
	RevokedAt  time.Time
	LastUsedAt time.Time
	CreatedAt  time.Time
}

// Touch ist eine gesammelte Aktualisierung von last_seen_at bzw. last_used_at.
type Touch struct {
	ID uuid.UUID
	At time.Time
}
