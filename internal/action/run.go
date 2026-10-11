// Package action enthält die Typen der Läufe; ausgeführt werden sie vom Paket executor.
package action

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
)

// ErrNotFound bedeutet, dass der Lauf nicht existiert.
var ErrNotFound = errors.New("run not found")

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

// Final meldet einen Endzustand; er ist endgültig.
func (s Status) Final() bool {
	switch s {
	case StatusRejected, StatusFailed, StatusConfirmed, StatusUnconfirmed, StatusCancelled:
		return true
	}
	return false
}

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
	ElementPath  string    // Anzeigename des Ziels beim Anlegen; hält den Lauf lesbar, wenn das Ziel gelöscht ist
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
