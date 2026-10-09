package evaluation

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Origin ist die Herkunft eines Befunds.
type Origin string

const (
	OriginRule   Origin = "rule"   // verletzte Regel des Users, Code rule.violated
	OriginDevice Origin = "device" // vom Gerät gemeldeter Alarm, Code = Alarmcode
	OriginSystem Origin = "system" // Monika selbst, z. B. source.unreachable
)

// Finding ist eine aktive oder vergangene Abweichung an einem Element.
type Finding struct {
	ID         uuid.UUID
	ElementID  uuid.UUID
	Origin     Origin
	RuleID     uuid.UUID // bei OriginRule; uuid.Nil, wenn die Regel gelöscht ist
	Code       string
	Severity   Severity
	Message    string
	Details    json.RawMessage
	CausedByID uuid.UUID // uuid.Nil = Ursache, sonst Symptom dieses Befunds

	RaisedAt  time.Time
	ClearedAt time.Time // Null-Zeit = aktiv
	AckedAt   time.Time
	AckedByID uuid.UUID
}

// ClearReason ist der Grund, aus dem ein Befund endet; er steht in details.cleared_reason.
// Nur ReasonResolved bedeutet „behoben“ (finding.cleared), alle anderen „zurückgezogen“
// (finding.withdrawn): Der Befund wird dauerhaft nicht mehr bewertet.
type ClearReason string

const (
	ReasonResolved       ClearReason = "resolved"
	ReasonRuleDeleted    ClearReason = "rule_deleted"
	ReasonRuleDisabled   ClearReason = "rule_disabled"
	ReasonRuleChanged    ClearReason = "rule_changed"
	ReasonUnmonitored    ClearReason = "unmonitored"
	ReasonElementMissing ClearReason = "element_missing"
	ReasonSourceDisabled ClearReason = "source_disabled"
	ReasonDeviceDeleted  ClearReason = "device_deleted"
)

// Withdrawn meldet, ob der Befund zurückgezogen statt behoben wurde.
func (r ClearReason) Withdrawn() bool { return r != ReasonResolved }
