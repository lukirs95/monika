package action

import (
	"context"
	"errors"
)

// ErrNotFound bedeutet, dass der Lauf nicht existiert.
var ErrNotFound = errors.New("run not found")

// Store persistiert Läufe. Jede Methode ist eine in sich atomare Operation.
type Store interface {
	// CreateRuns legt einen Lauf samt Kindern in einer Transaktion an; die Reihenfolge von
	// Eltern und Kindern im Slice spielt keine Rolle.
	CreateRuns(ctx context.Context, runs []Run) error
	// UpdateRun schreibt Status, Quelle, Antwort und Zeitpunkte eines Laufs. Unbekannt: ErrNotFound.
	UpdateRun(ctx context.Context, r Run) error
	// FinishInterruptedRuns schließt beim Start Läufe ab, die ein Neustart unterbrochen hat:
	// pending und sent werden failed, accepted wird unconfirmed, mit reason in der Antwort.
	FinishInterruptedRuns(ctx context.Context, reason string) (int, error)
}
