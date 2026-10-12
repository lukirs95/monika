package api

import (
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
)

// optional übersetzt einen leeren Wert in ein fehlendes Feld (null in der Spezifikation).
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// optionalID übersetzt uuid.Nil in ein fehlendes Feld.
func optionalID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// optionalTime übersetzt die Null-Zeit in ein fehlendes Feld; Zeitpunkte gehen in UTC raus (R8).
func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return new(t.UTC())
}

func toElement(e inventory.Element) oapi.Element {
	out := oapi.Element{
		ID: e.ID, Kind: string(e.Kind), ParentID: optionalID(e.ParentID), DeviceID: optionalID(e.DeviceID),
		SourceID: optionalID(e.SourceID), ExternalID: optional(e.ExternalID), GlobalRef: optional(e.GlobalRef),
		Transport: optional(e.Transport), Name: optional(e.Name), Label: optional(e.Label), Notes: optional(e.Notes),
		Position: e.Position, Attributes: e.Attributes, Params: e.Params, Settings: e.Settings,
		Actions: nonNil(e.Actions), CriticalActions: nonNil(e.CriticalActions),
		Present: e.Present, Monitored: e.Monitored,
		FirstSeenAt: optionalTime(e.FirstSeenAt), LastSeenAt: optionalTime(e.LastSeenAt),
	}
	if e.Direction != "" {
		out.Direction = new(oapi.ElementDirection(e.Direction))
	}
	return out
}

func toSkeleton(e inventory.Element) oapi.SkeletonElement {
	return oapi.SkeletonElement{
		ID: e.ID, Kind: string(e.Kind), Name: optional(e.Name), Label: optional(e.Label),
		ParentID: optionalID(e.ParentID), DeviceID: optionalID(e.DeviceID),
	}
}

func toLink(l inventory.Link) oapi.Link {
	return oapi.Link{
		ID: l.ID, Type: oapi.LinkType(l.Type), SourceID: optionalID(l.SourceID),
		FromID: optionalID(l.FromID), FromRef: optional(l.FromRef), ToID: optionalID(l.ToID), ToRef: optional(l.ToRef),
		Position: l.Position, Label: optional(l.Label),
	}
}

func toHealth(h evaluation.ElementHealth) oapi.ElementHealth {
	out := oapi.ElementHealth{ElementID: h.ElementID}
	if h.Health != evaluation.HealthNone {
		out.Health = new(oapi.ElementHealthHealth(h.Health))
	}
	if r := h.Rollup; r != nil {
		out.Rollup = &oapi.Rollup{
			Ok: r.OK, Warning: r.Warning, Error: r.Error, Unknown: r.Unknown,
			Unchecked: r.Unchecked, Unmonitored: r.Unmonitored, Missing: r.Missing,
		}
	}
	return out
}

func toFinding(f evaluation.Finding) oapi.Finding {
	return oapi.Finding{
		ID: f.ID, ElementID: f.ElementID, ElementPath: f.ElementPath, Origin: oapi.FindingOrigin(f.Origin),
		RuleID: optionalID(f.RuleID), Code: f.Code, Severity: oapi.FindingSeverity(f.Severity),
		Message: optional(f.Message), Details: f.Details, CausedByID: optionalID(f.CausedByID),
		RaisedAt: f.RaisedAt.UTC(), ClearedAt: optionalTime(f.ClearedAt),
		AckedAt: optionalTime(f.AckedAt), AckedByID: optionalID(f.AckedByID),
	}
}

func toRunSummary(r action.Run) oapi.RunSummary {
	return oapi.RunSummary{
		ID: r.ID, ParentID: optionalID(r.ParentID), PresetID: optionalID(r.PresetID),
		ElementID: optionalID(r.ElementID), ElementPath: optional(r.ElementPath),
		Action: r.Action, Status: oapi.RunSummaryStatus(r.Status), Critical: new(r.Critical),
		RequestedByID: r.RequestedByID, RequestedAt: r.RequestedAt.UTC(), FinishedAt: optionalTime(r.FinishedAt),
	}
}

func toSourceStatus(id uuid.UUID, s core.SourceStatus) oapi.SourceStatus {
	out := oapi.SourceStatus{
		SourceID: id, State: oapi.SourceStatusState(s.State), Since: optionalTime(s.Since),
		LastError: optional(s.Error),
	}
	if s.State != core.StateDisabled && s.State != core.StateStarting { // sonst läuft keine Instanz
		out.HeartbeatSeconds = new(s.Heartbeat.Seconds())
	}
	return out
}

// nonNil liefert für eine fehlende Liste eine leere: Pflichtfelder sind [] statt null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// value übersetzt ein fehlendes Feld in den leeren Wert.
func value[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// valueID übersetzt eine fehlende ID in uuid.Nil.
func valueID(p *uuid.UUID) uuid.UUID { return value(p) }

func toSource(s inventory.Source) oapi.Source {
	out := oapi.Source{
		ID: s.ID, DeviceID: s.DeviceID, AccessElementID: s.AccessElementID, Driver: s.Driver,
		Address: optional(s.Address), Config: s.Config, SecretID: optionalID(s.SecretID), Enabled: s.Enabled,
	}
	if len(out.Config) == 0 {
		out.Config = []byte(`{}`)
	}
	if id := s.Identity; id != (inventory.Identity{}) {
		out.Identity = &oapi.Identity{Vendor: optional(id.Vendor), Model: optional(id.Model), Serial: optional(id.Serial), Firmware: optional(id.Firmware)}
	}
	return out
}
