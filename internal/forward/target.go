// Package forward leitet Ereignisse an syslog und webhook weiter (best effort).
package forward

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
)

// Kind ist die Art eines Weiterleitungsziels.
type Kind string

const (
	KindSyslog  Kind = "syslog"
	KindWebhook Kind = "webhook"
)

// Ereignisse, die ein Ziel abonnieren kann.
const (
	EventFindingRaised    = "finding.raised"
	EventFindingCleared   = "finding.cleared"
	EventFindingWithdrawn = "finding.withdrawn"
	EventFindingAcked     = "finding.acked"
	EventActionFinished   = "action.finished"
)

// Target ist ein Weiterleitungsziel mit Filtern und dem Ergebnis des letzten Versands.
type Target struct {
	ID       uuid.UUID
	Name     string
	Kind     Kind
	Enabled  bool
	Config   json.RawMessage // syslog: host, port, protocol, facility; webhook: url, method, headers, body
	SecretID uuid.UUID

	Events         []string
	MinSeverity    evaluation.Severity // leer = alle
	ScopeElementID uuid.UUID           // uuid.Nil = alles

	LastSentAt time.Time
	LastError  string
}

// Result ist das Ergebnis des letzten Versands an ein Ziel. Error leer = Erfolg.
type Result struct {
	TargetID uuid.UUID
	At       time.Time
	Error    string
}
