// Package history beschreibt das Lesen des Verlaufs: Audit, vergangene Befunde und Läufe, mit
// Filtern und Cursor-Paginierung (docs/contracts/api.md, Filter im Verlauf). Gelesen wird über
// eigene, rein lesende Queries; den aktuellen Stand liefert die View des Core.
package history

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
)

// Page ist eine Seite (R5): neueste zuerst, nur Einträge älter als Before (UUIDv7), höchstens
// Limit; From (einschließlich) und To (ausschließlich) begrenzen den Zeitraum, Null-Zeit = offen.
type Page struct {
	Before   uuid.UUID
	Limit    int
	From, To time.Time
}

// Sight schränkt auf sichtbare Elemente ein (E8): All bei view global, sonst die IDs in Visible.
// Elements ist der Filter element (mit recursive aufgelöst); nil = alle, eine leere Liste = keins.
type Sight struct {
	All      bool
	Visible  []uuid.UUID
	Elements []uuid.UUID
}

// FindingState ist der Filter state im Befundverlauf.
type FindingState string

const (
	FindingActive    FindingState = "active"
	FindingCleared   FindingState = "cleared"   // behoben (cleared_reason resolved)
	FindingWithdrawn FindingState = "withdrawn" // zurückgezogen (jeder andere Grund)
)

// FindingFilter filtert den Befundverlauf; leere Felder filtern nicht.
type FindingFilter struct {
	Page
	Sight
	Code     string
	Severity evaluation.Severity
	Origin   evaluation.Origin
	RuleID   uuid.UUID
	State    FindingState
}

// RunFilter filtert den Laufverlauf; leere Felder filtern nicht.
type RunFilter struct {
	Page
	Sight
	Roots       bool // Wurzel-Läufe von Presets (ohne Ziel) mit aufnehmen
	Status      action.Status
	Action      string
	PresetID    uuid.UUID
	RequestedBy uuid.UUID
}

// AuditFilter filtert das Audit; leere Felder filtern nicht.
type AuditFilter struct {
	Page
	ObjectType string
	ObjectID   uuid.UUID
	Actor      uuid.UUID
	System     bool // nur Einträge ohne Akteur (Monika selbst, etwa die Discovery)
	Verb       string
}

// AuditEntry ist ein Eintrag im Audit; Geheimnisse sind darin bereits geschwärzt.
type AuditEntry struct {
	ID         uuid.UUID
	ActorID    uuid.UUID // uuid.Nil = Monika selbst
	At         time.Time
	Verb       string
	ObjectType string
	ObjectID   uuid.UUID
	Before     json.RawMessage
	After      json.RawMessage
}
