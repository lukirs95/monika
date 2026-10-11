package executor

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

const defaultVerifyTimeout = 10 * time.Second

// planner löst einen Auftrag gegen eine View in Läufe auf, ohne etwas zu speichern.
type planner struct {
	drivers func(name string) (sdk.Driver, bool)
	newID   func() uuid.UUID
}

// plan ist das Ergebnis: runs[0] ist der Lauf, dessen ID der Aufrufer bekommt; ein
// übergeordneter Lauf steht vor seinen Kindern.
type plan struct {
	runs []action.Run
}

// summary steht im response eines übergeordneten Laufs (docs/contracts/services.md).
type summary struct {
	Targets     int                   `json:"targets"`
	Unsupported int                   `json:"unsupported,omitempty"`
	Skipped     []skipped             `json:"skipped,omitempty"`
	Statuses    map[action.Status]int `json:"statuses,omitempty"`
}

type skipped struct {
	ElementID   uuid.UUID `json:"element_id"`
	ElementPath string    `json:"element_path"`
	Reason      string    `json:"reason"`
}

// plan löst den Auftrag auf (docs/ARCHITECTURE.md, Abschnitt 9): Ein Element, das die Aktion
// selbst unterstützt, bekommt sie selbst; Gerät und Gruppe verteilen an die Zugangs-Elemente
// (DeviceLevel) bzw. an alle übrigen Elemente, Driver filtert; vorgegebene Targets ersetzen die
// Auflösung.
func (p planner) plan(v *core.View, r Request, now time.Time) (plan, error) {
	if r.Action == ActionPreset {
		return plan{runs: []action.Run{p.run(r, uuid.Nil, now)}}, nil
	}
	if r.Action == "" {
		return plan{}, fmt.Errorf("%w: no action", ErrInvalidParams)
	}
	setKeys, err := setKeysOf(r)
	if err != nil {
		return plan{}, err
	}
	g := v.Graph
	if r.Targets != nil {
		return p.collective(g, r, setKeys, now, r.Targets, true)
	}
	el, ok := g.Element(r.ElementID)
	if !ok {
		return plan{}, fmt.Errorf("%w: %s", ErrUnknownTarget, r.ElementID)
	}
	has, missing := supports(el, r.Action, setKeys)
	switch {
	case has && missing == "":
		child, err := p.child(g, r, el, r.ParentID, now)
		if err != nil {
			return plan{}, err
		}
		return plan{runs: []action.Run{child}}, nil
	case has:
		return plan{}, fmt.Errorf("%w: %s has no setting %q", ErrUnsupported, g.Path(el.ID), missing)
	case el.Kind != inventory.KindDevice && el.Kind != inventory.KindGroup:
		return plan{}, fmt.Errorf("%w: %s does not support %q", ErrUnsupported, g.Path(el.ID), r.Action)
	case r.DeviceLevel && r.Driver == "":
		return plan{}, ErrDriverRequired
	}
	var candidates []uuid.UUID
	for id := range g.ScopeElements(el.ID) {
		c, _ := g.Element(id)
		if c.Kind == inventory.KindDevice || c.Kind == inventory.KindGroup || (c.Kind == inventory.KindAccess) != r.DeviceLevel {
			continue
		}
		if src, _ := g.Source(c.SourceID); r.Driver != "" && src.Driver != r.Driver {
			continue
		}
		candidates = append(candidates, id)
	}
	slices.SortFunc(candidates, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return p.collective(g, r, setKeys, now, candidates, false)
}

// collective legt einen übergeordneten Lauf mit einem Kind pro Ziel an. frozen: vom Preset
// eingefrorene Ziele; ein gelöschtes Ziel bekommt dann einen Lauf, der als failed endet.
func (p planner) collective(g *inventory.Graph, r Request, setKeys []string, now time.Time, targets []uuid.UUID, frozen bool) (plan, error) {
	parent := p.run(r, r.ParentID, now)
	parent.ElementPath = g.Path(r.ElementID)
	sum := summary{}
	runs := []action.Run{parent}
	for _, id := range targets {
		el, ok := g.Element(id)
		if !ok {
			child := p.run(r, parent.ID, now)
			child.ElementID = id
			runs = append(runs, failed(child, now, "element not found"))
			continue
		}
		has, missing := supports(el, r.Action, setKeys)
		switch {
		case !has:
			sum.Unsupported++
			continue
		case missing != "":
			sum.Skipped = append(sum.Skipped, skipped{ElementID: id, ElementPath: g.Path(id), Reason: fmt.Sprintf("missing key %s", missing)})
			continue
		}
		child, err := p.child(g, r, el, parent.ID, now)
		if err != nil {
			return plan{}, err
		}
		runs = append(runs, child)
	}
	sum.Targets = len(runs) - 1
	if sum.Targets == 0 && !frozen {
		return plan{}, fmt.Errorf("%w: nothing in %s supports %q", ErrUnsupported, g.Path(r.ElementID), r.Action)
	}
	for _, c := range runs[1:] {
		runs[0].Critical = runs[0].Critical || c.Critical
	}
	b, _ := json.Marshal(sum) // nur einfache Typen
	runs[0].Response = b
	if sum.Targets == 0 {
		runs[0] = failed(runs[0], now, "no target supports the action")
		runs[0].Response = b
	}
	return plan{runs: runs}, nil
}

// child ist der Lauf an einem Element: kritisch und Prüfbedingung eingefroren; ein verschwundenes
// Element bekommt einen Lauf, der sofort als failed endet.
func (p planner) child(g *inventory.Graph, r Request, el inventory.Element, parent uuid.UUID, now time.Time) (action.Run, error) {
	run := p.run(r, parent, now)
	run.ElementID, run.ElementPath, run.SourceID = el.ID, g.Path(el.ID), el.SourceID
	src, _ := g.Source(el.SourceID)
	drv, ok := p.drivers(src.Driver)
	var def sdk.ActionDef
	if ok {
		i := slices.IndexFunc(drv.Actions, func(d sdk.ActionDef) bool { return d.Name == r.Action })
		ok = i >= 0
		if ok {
			def = drv.Actions[i]
		}
	}
	if !ok {
		return action.Run{}, fmt.Errorf("%w: driver %q has no action %q", ErrUnsupported, src.Driver, r.Action)
	}
	run.Critical = def.Critical || slices.Contains(el.CriticalActions, r.Action)
	timeout := def.Timeout
	if timeout <= 0 {
		timeout = defaultVerifyTimeout
	}
	var conds []evaluation.Condition
	if r.Action == vocab.ActionSet {
		settings := settingsOf(el)
		values := map[string]json.RawMessage{}
		_ = json.Unmarshal(r.Params, &values) // geprüft in setKeysOf
		for _, key := range slices.Sorted(maps.Keys(values)) {
			s := settings[key]
			run.Critical = run.Critical || s.Critical
			if !s.WriteOnly {
				conds = append(conds, evaluation.Condition{Fact: key, Op: evaluation.OpEq, Value: values[key]})
			}
		}
	} else {
		for _, c := range def.Verify {
			value, err := substitute(c.Value, r.Params)
			if err != nil {
				return action.Run{}, err
			}
			conds = append(conds, evaluation.Condition{Fact: c.Fact, Op: evaluation.Op(c.Op), Value: value})
		}
	}
	if len(conds) > 0 {
		run.Verify = &action.Verify{Timeout: timeout, Conditions: conds}
	}
	if !el.Present {
		return failed(run, now, "element not present"), nil
	}
	return run, nil
}

func (p planner) run(r Request, parent uuid.UUID, now time.Time) action.Run {
	return action.Run{
		ID: p.newID(), ParentID: parent, PresetID: r.PresetID, PresetStepID: r.PresetStepID,
		ElementID: r.ElementID, Action: r.Action, Params: r.Params, Status: action.StatusPending,
		RequestedByID: r.Actor, RequestedAt: now,
	}
}

// failed beendet einen Lauf schon beim Anlegen.
func failed(r action.Run, now time.Time, reason string) action.Run {
	r.Status, r.FinishedAt = action.StatusFailed, now
	r.Response, _ = json.Marshal(map[string]string{"error": reason})
	return r
}

// supports meldet, ob das Element die Aktion hat, und bei set den ersten fehlenden Schlüssel.
func supports(el inventory.Element, act string, setKeys []string) (has bool, missing string) {
	if !slices.Contains(el.Actions, act) {
		return false, ""
	}
	if act == vocab.ActionSet {
		settings := settingsOf(el)
		for _, k := range setKeys {
			if _, ok := settings[k]; !ok {
				return true, k
			}
		}
	}
	return true, ""
}

func settingsOf(el inventory.Element) map[string]sdk.Setting {
	var s map[string]sdk.Setting
	_ = json.Unmarshal(el.Settings, &s) // vom Inventar aus sdk.Setting erzeugt
	return s
}

// setKeysOf liefert bei set die gesetzten Schlüssel; Params muss ein nicht leeres Objekt sein.
func setKeysOf(r Request) ([]string, error) {
	if r.Action != vocab.ActionSet {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(r.Params, &values); err != nil || len(values) == 0 {
		return nil, fmt.Errorf("%w: set needs an object with at least one setting", ErrInvalidParams)
	}
	return slices.Sorted(maps.Keys(values)), nil
}

// substitute ersetzt {{name}} im Wert einer Prüfbedingung durch den JSON-Wert des Parameters.
func substitute(value string, params json.RawMessage) (json.RawMessage, error) {
	if !strings.Contains(value, "{{") {
		return json.RawMessage(value), nil
	}
	var values map[string]json.RawMessage
	_ = json.Unmarshal(params, &values)
	var out strings.Builder
	for rest := value; ; {
		i := strings.Index(rest, "{{")
		if i < 0 {
			out.WriteString(rest)
			break
		}
		j := strings.Index(rest[i:], "}}")
		if j < 0 {
			return nil, fmt.Errorf("%w: unclosed placeholder in %q", ErrInvalidParams, value)
		}
		name := strings.TrimSpace(rest[i+2 : i+j])
		v, ok := values[name]
		if !ok {
			return nil, fmt.Errorf("%w: missing parameter %q", ErrInvalidParams, name)
		}
		out.WriteString(rest[:i])
		out.Write(v)
		rest = rest[i+j+2:]
	}
	return json.RawMessage(out.String()), nil
}
