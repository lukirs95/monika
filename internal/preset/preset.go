// Package preset verwaltet und startet Presets: gespeicherte Befehlsfolgen in Stufen, kein Sollzustand.
package preset

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Preset ist eine gespeicherte Befehlsfolge. Schritte derselben Stufe laufen parallel, Stufen nacheinander.
type Preset struct {
	ID          uuid.UUID
	Name        string
	Description string
	CreatedByID uuid.UUID
	Steps       []Step // sortiert nach Stage, Position; leer, wenn nur Metadaten geladen wurden
}

// Step ist ein Schritt eines Presets.
type Step struct {
	ID              uuid.UUID
	Stage           int           // ab 1
	Position        int           // Sortierung innerhalb der Stufe, nur Anzeige
	Delay           time.Duration // Wartezeit ab Beginn der Stufe, auf Millisekunden genau gespeichert
	ElementID       uuid.UUID     // Element, Gerät oder Gruppe
	Action          string
	Params          json.RawMessage
	ContinueOnError bool
	Driver          string // Pflicht bei Aktionen für das ganze Gerät auf Geräte oder Gruppen
}
