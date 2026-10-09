// Package action führt Aktionen aus: Läufe, Warteschlange pro Element, Prüfbedingung.
package action

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
)

// Status ist der Zustand eines Laufs (docs/contracts/flows.md, Lebenszyklus eines Laufs).
type Status string

const (
	StatusPending     Status = "pending"
	StatusSent        Status = "sent"
	StatusRejected    Status = "rejected"
	StatusFailed      Status = "failed"
	StatusAccepted    Status = "accepted"
	StatusConfirmed   Status = "confirmed"
	StatusUnconfirmed Status = "unconfirmed"
	StatusCancelled   Status = "cancelled"
)

// Verify ist die Prüfbedingung eines Laufs: alle Conditions müssen innerhalb von Timeout gelten.
type Verify struct {
	Timeout    time.Duration
	Conditions []evaluation.Condition
}

// Run ist eine Ausführung einer Aktion, eines Preset-Schritts oder eines Presets. Läufe sind
// Verlauf: Ihr Endzustand ist endgültig.
type Run struct {
	ID           uuid.UUID
	ParentID     uuid.UUID
	PresetID     uuid.UUID
	PresetStepID uuid.UUID
	ElementID    uuid.UUID // uuid.Nil nur am Wurzel-Lauf eines Presets
	SourceID     uuid.UUID // ausführende Quelle; uuid.Nil bei übergeordneten Läufen

	Action   string
	Params   json.RawMessage
	Status   Status
	Critical bool    // eingefroren beim Anlegen
	Verify   *Verify // nil = keine Prüfbedingung
	Response json.RawMessage

	RequestedByID uuid.UUID
	RequestedAt   time.Time
	SentAt        time.Time
	RespondedAt   time.Time
	FinishedAt    time.Time
}
