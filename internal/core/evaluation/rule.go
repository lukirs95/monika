package evaluation

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

// Severity ist der Schweregrad eines Befunds bzw. einer Regel.
type Severity string

const (
	SeverityInfo    Severity = "info" // färbt nicht
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Op ist der Vergleichsoperator einer Regel.
type Op string

const (
	OpEq    Op = "eq"     // bool, Zahl, String
	OpNe    Op = "ne"     // bool, Zahl, String
	OpLt    Op = "lt"     // Zahl
	OpLte   Op = "lte"    // Zahl
	OpGt    Op = "gt"     // Zahl
	OpGte   Op = "gte"    // Zahl
	OpIn    Op = "in"     // Value ist eine Liste
	OpNotIn Op = "not_in" // Value ist eine Liste
)

// Condition ist eine Bedingung der Form fact <op> value.
type Condition struct {
	Fact  string
	Op    Op
	Value json.RawMessage
}

// Check prüft die Bedingung gegen die Fakten eines Elements (Prüfbedingung eines Laufs), mit
// demselben Vergleich wie bei Regeln. Fehlt der Fakt oder passt sein Typ nicht, ist sie nicht
// erfüllt; ein ungültiger Wert ist ein Fehler.
func (c Condition) Check(facts sdk.Facts) (bool, error) {
	v, err := parseValue(c.Value)
	if err != nil {
		return false, err
	}
	f, ok := facts[c.Fact]
	if !ok {
		return false, nil
	}
	h, ok := holds(c.Op, f, v)
	return ok && h, nil
}

// Rule beschreibt einen Sollzustand: Fact Op Value, optional nur wenn When erfüllt ist.
// Geltungsbereich und Filter leer = global.
type Rule struct {
	ID       uuid.UUID
	Name     string
	Enabled  bool
	Severity Severity

	ScopeElementID uuid.UUID
	MatchKind      string
	MatchTransport string
	MatchDriver    string

	Fact  string
	Op    Op
	Value json.RawMessage
	When  *Condition // nil = gilt immer

	ClearValue       json.RawMessage // Hysterese, nur numerisch
	HoldSeconds      int             // Verletzung so lange, bevor ein Befund entsteht
	ClearHoldSeconds int             // Sollzustand so lange, bevor der Befund endet
}
