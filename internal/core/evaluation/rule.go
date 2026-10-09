package evaluation

import (
	"encoding/json"

	"github.com/google/uuid"
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

	ClearValue  json.RawMessage // Hysterese, nur numerisch
	HoldSeconds int
}
