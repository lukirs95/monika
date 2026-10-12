package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/forward"
	"github.com/lukirs95/monika/internal/preset"
	"github.com/lukirs95/monika/internal/secret"
)

// Problem ist eine Fehlerantwort nach RFC 9457 mit stabilem Code (docs/contracts/api.md, R4).
// Handler liefern es als Fehler; Fehler der Fachpakete übersetzt toProblem.
type Problem struct {
	Type    string           `json:"type"`
	Title   string           `json:"title"`
	Status  int              `json:"status"`
	Code    string           `json:"code"`
	Detail  string           `json:"detail,omitempty"`
	Presets []oapi.PresetRef `json:"presets,omitzero"` // bei *_in_use; leer, wenn der Benutzer keine Presets sehen darf
	Missing []missingRight   `json:"missing,omitzero"` // bei missing_rights

	retryAfter time.Duration // bei 429: Header Retry-After
}

func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

// problem baut ein Problem; detail ist für Menschen und enthält nie Geheimnisse.
func problem(status int, code, detail string) *Problem {
	return &Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, Detail: detail}
}

// Fehler, die die API selbst erzeugt.
var (
	errUnauthenticated = problem(http.StatusUnauthorized, "unauthenticated", "not authenticated")
	errReadOnly        = problem(http.StatusForbidden, "read_only", "read-only access")
	errOrigin          = problem(http.StatusForbidden, "origin_mismatch", "origin does not match the public URL")
	errNotImplemented  = problem(http.StatusNotImplemented, "not_implemented", "not implemented yet")
)

// codes ordnet fachlichen Fehlern Status und Code zu. Die Reihenfolge zählt nicht: Jeder Fehler
// steht genau einmal. Codes, die vom Endpunkt abhängen (etwa device_in_use), setzen die Handler
// selbst.
var codes = []struct {
	err    error
	status int
	code   string
}{
	{inventory.ErrNotFound, http.StatusNotFound, "not_found"},
	{evaluation.ErrNotFound, http.StatusNotFound, "not_found"},
	{auth.ErrNotFound, http.StatusNotFound, "not_found"},
	{secret.ErrNotFound, http.StatusNotFound, "not_found"},
	{preset.ErrNotFound, http.StatusNotFound, "not_found"},
	{forward.ErrNotFound, http.StatusNotFound, "not_found"},
	{action.ErrNotFound, http.StatusNotFound, "not_found"},
	{executor.ErrUnknownTarget, http.StatusNotFound, "not_found"},

	{inventory.ErrSourceExists, http.StatusConflict, "source_exists"},
	{inventory.ErrMemberExists, http.StatusConflict, "member_exists"},
	{inventory.ErrMemberCycle, http.StatusConflict, "member_cycle"},
	{auth.ErrNameTaken, http.StatusConflict, "name_taken"},
	{secret.ErrNameTaken, http.StatusConflict, "name_taken"},
	{secret.ErrInUse, http.StatusConflict, "secret_in_use"},
	{auth.ErrTOTPConfirmed, http.StatusConflict, "totp_confirmed"},
	{executor.ErrFinished, http.StatusConflict, "run_finished"},

	{inventory.ErrSecretNotFound, http.StatusUnprocessableEntity, "invalid_secret"},
	{inventory.ErrUnknownDriver, http.StatusUnprocessableEntity, "unknown_driver"},
	{inventory.ErrInvalidConfig, http.StatusUnprocessableEntity, "invalid_config"},
	{inventory.ErrNotAGroup, http.StatusUnprocessableEntity, "not_a_group"},
	{evaluation.ErrInvalidRule, http.StatusUnprocessableEntity, "invalid_rule"},
	{executor.ErrUnsupported, http.StatusUnprocessableEntity, "unsupported_action"},
	{executor.ErrDriverRequired, http.StatusUnprocessableEntity, "driver_required"},
	{preset.ErrDriverRequired, http.StatusUnprocessableEntity, "driver_required"},
	{executor.ErrInvalidParams, http.StatusUnprocessableEntity, "invalid_params"},
	{preset.ErrInvalidTarget, http.StatusUnprocessableEntity, "invalid_target"},
	{preset.ErrInvalidStep, http.StatusUnprocessableEntity, "invalid_step"},
	{forward.ErrSecretInBody, http.StatusUnprocessableEntity, "secret_in_body"}, // vor ErrInvalidTarget, das es umschließt
	{forward.ErrInvalidTarget, http.StatusUnprocessableEntity, "invalid_target"},
	{forward.ErrSecretNotFound, http.StatusUnprocessableEntity, "invalid_secret"},
	{secret.ErrInvalidName, http.StatusUnprocessableEntity, "invalid_secret"},
	{secret.ErrInvalidValue, http.StatusUnprocessableEntity, "invalid_secret"},
	{auth.ErrWeakPassword, http.StatusUnprocessableEntity, "weak_password"},
	{auth.ErrLongPassword, http.StatusUnprocessableEntity, "weak_password"},
	{auth.ErrInvalidUser, http.StatusUnprocessableEntity, "invalid_user"},
	{auth.ErrInvalidRole, http.StatusUnprocessableEntity, "invalid_role"},
	{auth.ErrInvalidGrant, http.StatusUnprocessableEntity, "global_permission"},

	{auth.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
	{auth.ErrInvalidChallenge, http.StatusUnauthorized, "invalid_challenge"},
	{auth.ErrInvalidCode, http.StatusUnauthorized, "invalid_code"},
	{auth.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
	{preset.ErrForbidden, http.StatusForbidden, "missing_rights"},
	{executor.ErrNotPresetRoot, http.StatusConflict, "not_preset_root"},

	{core.ErrStopped, http.StatusServiceUnavailable, "unavailable"},
	{executor.ErrStopped, http.StatusServiceUnavailable, "unavailable"},
	{preset.ErrStopped, http.StatusServiceUnavailable, "unavailable"},
}

// toProblem übersetzt einen Fehler in ein Problem. Unbekannte Fehler sind 500 ohne Details; der
// Aufrufer loggt sie.
func toProblem(err error) *Problem {
	if p, ok := errors.AsType[*Problem](err); ok {
		return p
	}
	if t, ok := errors.AsType[auth.ThrottledError](err); ok {
		p := problem(http.StatusTooManyRequests, "throttled", "too many failed attempts")
		p.retryAfter = t.Wait
		return p
	}
	for _, c := range codes {
		if errors.Is(err, c.err) {
			return problem(c.status, c.code, err.Error())
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return problem(http.StatusServiceUnavailable, "unavailable", "request cancelled")
	}
	return problem(http.StatusInternalServerError, "internal", "internal error")
}

// missingRight ist ein fehlendes Recht beim Start eines Presets (ProblemMissingRights).
type missingRight struct {
	StepID     *uuid.UUID `json:"step_id,omitempty"` // leer, wenn preset_run selbst fehlt
	ElementID  *uuid.UUID `json:"element_id,omitempty"`
	Permission string     `json:"permission"`
}

// missingRights übersetzt *preset.MissingRightsError in 403 missing_rights mit allen Einträgen.
func missingRights(err error) error {
	e, ok := errors.AsType[*preset.MissingRightsError](err)
	if !ok {
		return err
	}
	p := problem(http.StatusForbidden, "missing_rights", "missing permissions for preset targets")
	for _, m := range e.Missing {
		p.Missing = append(p.Missing, missingRight{StepID: optionalID(m.StepID), ElementID: optionalID(m.ElementID), Permission: string(m.Permission)})
	}
	return p
}

// inUse übersetzt *inventory.InUseError in 409 mit dem Code des Endpunkts; die Presets nennt es
// nur, wenn der Benutzer Presets sehen darf (showPresets).
func inUse(err error, code string, showPresets bool) error {
	e, ok := errors.AsType[*inventory.InUseError](err)
	if !ok {
		return err
	}
	p := problem(http.StatusConflict, code, "used by presets")
	p.Presets = []oapi.PresetRef{}
	for _, ref := range e.Presets {
		if !showPresets {
			break
		}
		p.Presets = append(p.Presets, oapi.PresetRef{ID: ref.ID, Name: ref.Name})
	}
	return p
}

// writeProblem schreibt ein Problem als application/problem+json.
func writeProblem(w http.ResponseWriter, p *Problem) {
	h := w.Header()
	h.Set("Content-Type", "application/problem+json")
	if p.retryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(int((p.retryAfter+time.Second-1)/time.Second)))
	}
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
