package sdk

import "encoding/json"

// Facts sind die Fakten eines Elements: Schlüssel <measurement>.<feld> aus dem Vokabular oder
// <treiber>.<name> aus dem Katalog; Werte genau vom deklarierten Go-Typ (bool, int64, float64,
// string).
type Facts map[string]any

// Snapshot ist der vollständige Stand einer Quelle. Schlüssel von Facts und Alarms ist die
// ExternalID; "" ist das Zugangs-Element.
type Snapshot struct {
	Identity  Identity
	GlobalRef string // globale ID des Geräts aus Sicht dieser Quelle (Namensraum des Treibers)
	Elements  []Element
	Links     []Link
	Facts     map[string]Facts
	Alarms    map[string][]Alarm
}

// Update meldet geänderte Fakten und Alarme. Alarms ersetzt pro Element die vorige Liste; ein
// Element ohne Eintrag bleibt unverändert.
type Update struct {
	Facts  map[string]Facts
	Alarms map[string][]Alarm
}

// Identity ist die Identität des Geräts, wie diese Quelle sie sieht.
type Identity struct {
	Vendor   string
	Model    string
	Serial   string
	Firmware string
}

// Element ist ein Element aus Sicht der Quelle.
type Element struct {
	ExternalID      string // stabil, eindeutig in der Quelle; nie eine laufende Nummer
	ParentID        string // ExternalID der Eltern; "" = direkt unter dem Zugangs-Element
	Kind            string // vocab.Element*; nie device, group, access
	Transport       string
	Direction       string
	Name            string
	GlobalRef       string // nur am Element im eigenen Baum, mit Namensraum des Treibers
	Position        *int
	Attributes      json.RawMessage // nur zur Anzeige
	Params          map[string]map[string]Param
	Actions         []string
	CriticalActions []string
	Settings        map[string]Setting // Schlüssel = Fakt-Schlüssel
}

// Link-Typen, die Treiber melden; member vergibt nur der User.
const (
	LinkFeeds      = "feeds"      // fester Signalfluss; keine Aktion darf ihn ändern
	LinkUses       = "uses"       // wählbare Bindung
	LinkRepresents = "represents" // From ist Stellvertreter von To (immer per GlobalRef)
)

// Link ist eine Beziehung zwischen zwei Elementen.
type Link struct {
	Type string
	From Endpoint
	To   Endpoint
}

// Endpoint ist entweder ein Element dieser Quelle (ExternalID) oder eines anderen (GlobalRef).
type Endpoint struct {
	ExternalID string
	GlobalRef  string
}

// Severity ist der Schweregrad eines Alarms.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Alarm ist ein vom Gerät selbst gemeldeter Alarm.
type Alarm struct {
	Code     string
	Severity Severity
	Message  string
}
