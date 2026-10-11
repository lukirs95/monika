package forward

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
)

// message ist ein Ereignis im gemeinsamen Format von syslog und webhook
// (docs/ARCHITECTURE.md, Abschnitt 13).
type message struct {
	Event   string    `json:"event"`
	At      time.Time `json:"at"`
	Finding *finding  `json:"finding,omitempty"`
	Run     *run      `json:"run,omitempty"`
	AckedBy *user     `json:"acked_by,omitempty"`
	Element *element  `json:"element,omitempty"`

	severity evaluation.Severity // nur bei Befunden, für den Filter
	findID   uuid.UUID
}

type finding struct {
	ID            uuid.UUID              `json:"id"`
	ElementID     uuid.UUID              `json:"element_id"`
	Origin        evaluation.Origin      `json:"origin"`
	RuleID        *uuid.UUID             `json:"rule_id,omitempty"`
	Code          string                 `json:"code"`
	Severity      evaluation.Severity    `json:"severity"`
	Message       string                 `json:"message,omitempty"`
	Details       json.RawMessage        `json:"details,omitempty"`
	CausedByID    *uuid.UUID             `json:"caused_by_id,omitempty"`
	RaisedAt      time.Time              `json:"raised_at"`
	ClearedAt     *time.Time             `json:"cleared_at,omitempty"`
	ClearedReason evaluation.ClearReason `json:"cleared_reason,omitempty"`
	AckedAt       *time.Time             `json:"acked_at,omitempty"`
}

type run struct {
	ID            uuid.UUID       `json:"id"`
	ElementID     *uuid.UUID      `json:"element_id,omitempty"`
	Action        string          `json:"action"`
	Status        action.Status   `json:"status"`
	Critical      bool            `json:"critical"`
	PresetID      *uuid.UUID      `json:"preset_id,omitempty"`
	RequestedByID *uuid.UUID      `json:"requested_by_id,omitempty"`
	RequestedAt   time.Time       `json:"requested_at"`
	FinishedAt    time.Time       `json:"finished_at"`
	Response      json.RawMessage `json:"response,omitempty"`
}

type user struct {
	ID uuid.UUID `json:"id"`
}

type element struct {
	ID   uuid.UUID `json:"id"`
	Path string    `json:"path"`
	URL  string    `json:"url,omitempty"`
}

func optional(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// findingMessage baut ein Befund-Ereignis; reason ist nur beim Ende gesetzt.
func findingMessage(event string, at time.Time, f evaluation.Finding, reason evaluation.ClearReason, public *url.URL) message {
	m := message{
		Event: event, At: at, severity: f.Severity, findID: f.ID,
		Finding: &finding{
			ID: f.ID, ElementID: f.ElementID, Origin: f.Origin, RuleID: optional(f.RuleID), Code: f.Code,
			Severity: f.Severity, Message: f.Message, Details: f.Details, CausedByID: optional(f.CausedByID),
			RaisedAt: f.RaisedAt, ClearedAt: optionalTime(f.ClearedAt), ClearedReason: reason, AckedAt: optionalTime(f.AckedAt),
		},
		Element: elementOf(f.ElementID, f.ElementPath, public),
	}
	if event == EventFindingAcked {
		m.AckedBy = &user{ID: f.AckedByID}
	}
	return m
}

func runMessage(at time.Time, r action.Run, public *url.URL) message {
	return message{
		Event: EventActionFinished, At: at,
		Run: &run{
			ID: r.ID, ElementID: optional(r.ElementID), Action: r.Action, Status: r.Status, Critical: r.Critical,
			PresetID: optional(r.PresetID), RequestedByID: optional(r.RequestedByID), RequestedAt: r.RequestedAt,
			FinishedAt: r.FinishedAt, Response: r.Response,
		},
		Element: elementOf(r.ElementID, r.ElementPath, public),
	}
}

// elementOf: Link in die UI über MONIKA_PUBLIC_URL, Route /elements/<id>.
func elementOf(id uuid.UUID, path string, public *url.URL) *element {
	if id == uuid.Nil {
		return nil
	}
	e := &element{ID: id, Path: path}
	if public != nil {
		e.URL = public.JoinPath("elements", id.String()).String()
	}
	return e
}

// syslogSeverity nach Abschnitt 13: raised nach Schwere (error 3, warning 4, info 6),
// cleared/withdrawn 5, acked 6, action.finished 4 bei failed/rejected, sonst 6.
func (m message) syslogSeverity() int {
	switch m.Event {
	case EventFindingRaised:
		switch m.severity {
		case evaluation.SeverityError:
			return 3
		case evaluation.SeverityWarning:
			return 4
		}
		return 6
	case EventFindingCleared, EventFindingWithdrawn:
		return 5
	case EventActionFinished:
		if s := m.Run.Status; s == action.StatusFailed || s == action.StatusRejected {
			return 4
		}
	}
	return 6
}
