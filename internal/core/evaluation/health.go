// Package evaluation bewertet den Zustand: Regeln, Befunde, Ursache und Symptom, Health und Rollup.
package evaluation

import "github.com/google/uuid"

// Health ist der bewertete Zustand eines Elements.
type Health string

const (
	// HealthNone: keine Health, weil das Element nicht überwacht, verschwunden oder ungeprüft
	// ist; bei Gerät und Gruppe auch, wenn es nichts zusammenzufassen gibt.
	HealthNone    Health = ""
	HealthOK      Health = "ok"
	HealthWarning Health = "warning"
	HealthError   Health = "error"
	HealthUnknown Health = "unknown" // die Quelle ist nicht Live
)

// Rollup zählt die verschiedenen Elemente im Geltungsbereich eines Geräts oder einer Gruppe nach
// Health, jedes genau einmal. Unchecked, Unmonitored und Missing sind informativ und fließen
// nicht in die Health ein.
type Rollup struct {
	OK          int `json:"ok"`
	Warning     int `json:"warning"`
	Error       int `json:"error"`
	Unknown     int `json:"unknown"`
	Unchecked   int `json:"unchecked"` // ohne geltende Regel
	Unmonitored int `json:"unmonitored"`
	Missing     int `json:"missing"`
}

// ElementHealth ist die gespeicherte Bewertung eines Elements.
type ElementHealth struct {
	ElementID uuid.UUID
	Health    Health
	Rollup    *Rollup // nur bei Geräten und Gruppen
}
