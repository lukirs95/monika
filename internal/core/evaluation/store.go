package evaluation

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound bedeutet, dass die Regel bzw. der Befund nicht existiert.
var ErrNotFound = errors.New("not found")

// Store persistiert Regeln und die Ergebnisse der Bewertung. Jede Methode ist eine in sich
// atomare Operation.
//
// Methoden, die der User auslöst, bekommen den handelnden Benutzer als actor und schreiben
// einen Eintrag ins Audit; uuid.Nil steht für Monika selbst.
type Store interface {
	// ListRules lädt alle Regeln.
	ListRules(ctx context.Context) ([]Rule, error)
	CreateRule(ctx context.Context, actor uuid.UUID, r Rule) error
	// UpdateRule ändert eine Regel. Wird sie dabei deaktiviert, werden ihre aktiven Befunde
	// zurückgezogen (ReasonRuleDisabled). Was bei einer inhaltlichen Änderung endet, entscheidet
	// der Core bei der Neubewertung.
	UpdateRule(ctx context.Context, actor uuid.UUID, r Rule) error
	// DeleteRule zieht die aktiven Befunde der Regel zurück (ReasonRuleDeleted) und löscht sie;
	// ihre Befunde bleiben mit rule_id NULL erhalten.
	DeleteRule(ctx context.Context, actor uuid.UUID, id uuid.UUID) error

	// LoadHealth lädt Health und Rollup aller Elemente, die bereits bewertet wurden.
	LoadHealth(ctx context.Context) ([]ElementHealth, error)
	// SaveHealth schreibt Health und Rollup der übergebenen Elemente in einem Batch.
	// Unveränderte Werte lassen die Zeile unberührt.
	SaveHealth(ctx context.Context, hs []ElementHealth) error
}
