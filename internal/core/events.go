package core

import (
	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/sdk"
)

// Event ist eine Änderung, die die Schleife an ihre Abonnenten verteilt. Version ist die der View,
// mit der die Änderung veröffentlicht wurde.
type Event interface{ Version() uint64 }

// stamp trägt die Version ein, mit der eine Runde veröffentlicht wird.
type stamp struct{ version uint64 }

func (s stamp) Version() uint64 { return s.version }

type (
	// ElementsChanged: Das Inventar hat sich geändert (Discovery oder Befehl).
	ElementsChanged struct {
		stamp
		Added, Changed, Missing, Returned, Deleted []uuid.UUID
		LinksChanged                               bool
	}
	// FactsChanged: pro Element die geänderten Schlüssel (neu, geändert oder weggefallen) und die
	// neuen Fakten des Elements (unveränderlich, geteilt mit der View).
	FactsChanged struct {
		stamp
		Changed map[uuid.UUID][]string
		Facts   map[uuid.UUID]sdk.Facts
	}
	FindingRaised struct {
		stamp
		Finding evaluation.Finding
	}
	// FindingEnded: Der Befund ist behoben oder zurückgezogen; Finding mit ClearedAt.
	FindingEnded struct {
		stamp
		Finding evaluation.Finding
		Reason  evaluation.ClearReason
	}
	FindingAcked struct {
		stamp
		Finding evaluation.Finding
	}
	// FindingCauseChanged: neue Zuordnung; CausedBy uuid.Nil = selbst Ursache.
	FindingCauseChanged struct {
		stamp
		FindingID, CausedBy uuid.UUID
	}
	HealthChanged struct {
		stamp
		Health []evaluation.ElementHealth
	}
	SourceStatusChanged struct {
		stamp
		SourceID uuid.UUID
		Status   SourceStatus
	}
)

// WithVersion liefert das Ereignis mit der Version v. Die Schleife stempelt so die Ereignisse
// einer Runde; Tests von Abonnenten bauen damit Ereignisse.
func WithVersion(e Event, v uint64) Event {
	switch e := e.(type) {
	case ElementsChanged:
		e.version = v
		return e
	case FactsChanged:
		e.version = v
		return e
	case FindingRaised:
		e.version = v
		return e
	case FindingEnded:
		e.version = v
		return e
	case FindingAcked:
		e.version = v
		return e
	case FindingCauseChanged:
		e.version = v
		return e
	case HealthChanged:
		e.version = v
		return e
	case SourceStatusChanged:
		e.version = v
		return e
	case RunChanged:
		e.version = v
		return e
	}
	return e
}
