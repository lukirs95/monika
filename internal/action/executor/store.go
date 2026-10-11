package executor

import (
	"context"

	"github.com/lukirs95/monika/internal/action"
)

// Store persistiert Läufe. Jede Methode ist eine in sich atomare Operation.
type Store interface {
	// CreateRuns legt einen Lauf samt Kindern in einer Transaktion an; die Reihenfolge von
	// Eltern und Kindern im Slice spielt keine Rolle.
	CreateRuns(ctx context.Context, runs []action.Run) error
	// UpdateRun schreibt Status, Quelle, Antwort und Zeitpunkte eines Laufs. Unbekannt:
	// action.ErrNotFound.
	UpdateRun(ctx context.Context, r action.Run) error
	// FinishInterruptedRuns schließt beim Start Läufe ab, die ein Neustart unterbrochen hat:
	// pending und sent werden failed, accepted wird unconfirmed, mit reason in der Antwort.
	FinishInterruptedRuns(ctx context.Context, reason string) (int, error)
}
