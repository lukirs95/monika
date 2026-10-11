// Package auth beantwortet zwei getrennte Fragen: Wer bist du (Anmeldung, Sitzung, Token) und
// was darfst du (Rollen, Rechte, Geltungsbereich). Siehe docs/contracts/auth.md.
package auth

import (
	"time"

	"github.com/google/uuid"
)

// Permission ist ein Recht. preset_run, preset_edit, source_edit und admin gelten nur global.
type Permission string

const (
	PermView          Permission = "view"
	PermAcknowledge   Permission = "acknowledge"
	PermOperate       Permission = "operate"
	PermConfigure     Permission = "configure"
	PermCritical      Permission = "critical"
	PermInventoryEdit Permission = "inventory_edit"
	PermSourceEdit    Permission = "source_edit" // Quellen und Secret-Zuweisung; nur global, weil Secrets geteilt werden
	PermRuleEdit      Permission = "rule_edit"
	PermPresetRun     Permission = "preset_run"
	PermPresetEdit    Permission = "preset_edit"
	PermAdmin         Permission = "admin"
)

// Global meldet, ob das Recht nur ohne Geltungsbereich vergeben werden darf.
func (p Permission) Global() bool {
	return p == PermPresetRun || p == PermPresetEdit || p == PermSourceEdit || p == PermAdmin
}

// User ist ein Mensch oder ein Dienstkonto. Benutzer werden nie gelöscht, nur deaktiviert.
type User struct {
	ID           uuid.UUID
	Username     string // kleingeschrieben
	DisplayName  string
	Email        string
	PasswordHash string // argon2id; leer bei Dienstkonten und nur externer Anmeldung
	Service      bool
	Disabled     bool
	CreatedAt    time.Time
	LastLoginAt  time.Time
}

// Role ist eine Benutzergruppe, an die Rechte vergeben werden.
type Role struct {
	ID          uuid.UUID
	Name        string
	Description string
	ExternalRef string // Wert im Groups-Claim des IdP (später OIDC)
}

// Grant ist ein Recht mit Geltungsbereich; uuid.Nil = überall.
type Grant struct {
	Permission     Permission
	ScopeElementID uuid.UUID
}

// RoleGrant ist ein Grant einer bestimmten Rolle.
type RoleGrant struct {
	RoleID uuid.UUID
	Grant
}

// Membership ordnet einen Benutzer einer Rolle zu.
type Membership struct {
	RoleID  uuid.UUID
	UserID  uuid.UUID
	FromIdP bool // origin = idp: beim Login aus dem Groups-Claim synchronisiert
}

// Authz ist alles, was Allowed braucht; auth hält es im Speicher.
type Authz struct {
	Roles   []Role
	Grants  []RoleGrant
	Members []Membership
}

// Login beschreibt eine Anmeldung für das Audit.
type Login struct {
	UserID     uuid.UUID // uuid.Nil bei unbekanntem Benutzernamen
	Username   string
	RemoteAddr string
	Success    bool
	Reason     string // bei Fehlschlag, z. B. wrong_password, disabled, wrong_code
}
