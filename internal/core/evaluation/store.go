package evaluation

import "context"

// Store persistiert die Ergebnisse der Bewertung. Jede Methode ist eine in sich atomare Operation.
type Store interface {
	// LoadHealth lädt Health und Rollup aller Elemente, die bereits bewertet wurden.
	LoadHealth(ctx context.Context) ([]ElementHealth, error)
	// SaveHealth schreibt Health und Rollup der übergebenen Elemente in einem Batch.
	// Unveränderte Werte lassen die Zeile unberührt.
	SaveHealth(ctx context.Context, hs []ElementHealth) error
}
