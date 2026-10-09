package evaluation

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
