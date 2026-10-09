package preset

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	// ErrNotFound bedeutet, dass das Preset nicht existiert.
	ErrNotFound = errors.New("preset not found")
	// ErrInvalidTarget bedeutet, dass ein Schritt auf ein unbekanntes oder gelöschtes Element,
	// Gerät oder eine gelöschte Gruppe zielt bzw. eine Schritt-ID zu einem anderen Preset gehört.
	ErrInvalidTarget = errors.New("invalid preset step")
)

// Store persistiert Presets. Jede Methode ist eine in sich atomare Operation.
//
// Methoden, die der User auslöst, bekommen den handelnden Benutzer als actor und schreiben
// einen Eintrag ins Audit (Preset mit Schritten); uuid.Nil steht für Monika selbst.
type Store interface {
	// ListPresets liefert alle Presets ohne Schritte, sortiert nach Name.
	ListPresets(ctx context.Context) ([]Preset, error)
	// GetPreset liefert ein Preset mit Schritten. Unbekannt: ErrNotFound.
	GetPreset(ctx context.Context, id uuid.UUID) (Preset, error)
	// CreatePreset legt ein Preset mit Schritten an. Ungültige Ziele: ErrInvalidTarget.
	CreatePreset(ctx context.Context, actor uuid.UUID, p Preset) error
	// UpdatePreset ersetzt Name, Beschreibung und Schritte. Bestehende Schritte behalten ihre ID,
	// entfernte werden gelöscht. Unbekannt: ErrNotFound; ungültige Ziele: ErrInvalidTarget.
	UpdatePreset(ctx context.Context, actor uuid.UUID, p Preset) error
	// DeletePreset löscht ein Preset samt Schritten. Unbekannt: ErrNotFound.
	DeletePreset(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
}
