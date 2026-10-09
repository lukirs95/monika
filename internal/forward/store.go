package forward

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound bedeutet, dass das Ziel nicht existiert.
var ErrNotFound = errors.New("forward target not found")

// Store persistiert Weiterleitungsziele. Jede Methode ist eine in sich atomare Operation.
//
// Methoden, die der User auslöst, bekommen den handelnden Benutzer als actor und schreiben
// einen Eintrag ins Audit; uuid.Nil steht für Monika selbst.
type Store interface {
	// ListTargets liefert alle Ziele, sortiert nach Name.
	ListTargets(ctx context.Context) ([]Target, error)
	CreateTarget(ctx context.Context, actor uuid.UUID, t Target) error
	// UpdateTarget ändert ein Ziel. Unbekannt: ErrNotFound.
	UpdateTarget(ctx context.Context, actor uuid.UUID, t Target) error
	// DeleteTarget löscht ein Ziel. Unbekannt: ErrNotFound.
	DeleteTarget(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
	// SetTargetResults schreibt die Ergebnisse des letzten Versands in einem Batch: Erfolg setzt
	// last_sent_at und leert last_error, ein Fehler setzt nur last_error.
	SetTargetResults(ctx context.Context, rs []Result) error
}
