// Package vocab ist das Vokabular von Monika: Arten, Transporte, Richtungen, Fakten mit Typ und
// Art, Aktionen, Namensräume und reservierte Namen. Es ist die gemeinsame Sprache von Treibern,
// Core, Regeln, UI und Zeitreihen (docs/contracts/vocabulary.md).
//
// Neue Werte brauchen keine Migration, aber eine Änderung an diesem Paket mit Review.
package vocab

// Type ist der Typ eines Fakts; er bestimmt den Go-Typ des Werts und den Typ in InfluxDB.
type Type string

const (
	Bool   Type = "bool"   // Go bool
	Int    Type = "int"    // Go int64, InfluxDB Integer
	Float  Type = "float"  // Go float64
	String Type = "string" // Go string
)

// Valid meldet, ob t einer der vier Typen ist.
func (t Type) Valid() bool {
	switch t {
	case Bool, Int, Float, String:
		return true
	}
	return false
}

// Kind ist die Art eines Fakts.
type Kind string

const (
	Status  Kind = "status"  // Zustand oder Format
	Gauge   Kind = "gauge"   // Messwert
	Counter Kind = "counter" // Zähler; zu jedem gibt es die Rate <feld>_per_s
)

// Valid meldet, ob k eine der drei Arten ist.
func (k Kind) Valid() bool {
	switch k {
	case Status, Gauge, Counter:
		return true
	}
	return false
}

// RateSuffix kennzeichnet die Rate eines Zählers pro Sekunde: <feld>_per_s.
const RateSuffix = "_per_s"

// Present ist der reservierte Fakt, den der Core aus element.present einblendet.
const Present = "present"

// Reservierte Measurements, die Monika selbst schreibt.
const (
	MeasurementHealth = "health"
	MeasurementRollup = "rollup"
	MeasurementMonika = "monika"
)

// Reservierte Arten, die nur der Core anlegt.
const (
	ElementDevice = "device"
	ElementGroup  = "group"
	ElementAccess = "access"
)

// ReservedElementKind meldet, ob ein Treiber Elemente dieser Art nie melden darf.
func ReservedElementKind(kind string) bool {
	return kind == ElementDevice || kind == ElementGroup || kind == ElementAccess
}
