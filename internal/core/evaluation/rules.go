package evaluation

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// ErrInvalidRule bedeutet, dass eine Regel nicht zum Fakt-Katalog passt oder unvollständig ist.
var ErrInvalidRule = errors.New("invalid rule")

// FactTypes liefert den Typ eines Fakt-Schlüssels aus Vokabular und Katalogen der Treiber.
type FactTypes func(key string) (vocab.Type, bool)

// RegistryFactTypes schlägt im Vokabular und bei den registrierten Treibern nach (rohe Fakten
// tragen den Treibernamen als Präfix); present ist der reservierte Fakt vom Typ bool.
func RegistryFactTypes(key string) (vocab.Type, bool) {
	if key == vocab.Present {
		return vocab.Bool, true
	}
	if f, ok := vocab.Lookup(key); ok {
		return f.Type, true
	}
	driver, _, ok := strings.Cut(key, ".")
	if !ok {
		return "", false
	}
	d, ok := sdk.Lookup(driver)
	if !ok {
		return "", false
	}
	return d.FactType(key)
}

// rule ist eine geprüfte Regel mit gelesenen Werten.
type rule struct {
	Rule
	value any // bool, num, string oder []any
	clear any // num; nil = ohne Hysterese
	when  *cond
}

type cond struct {
	fact  string
	op    Op
	value any
}

// compileRule prüft eine Regel gegen den Fakt-Katalog und liest ihre Werte.
func compileRule(r Rule, types FactTypes) (*rule, error) {
	var errs []error
	if r.Name == "" {
		errs = append(errs, errors.New("name is empty"))
	}
	switch r.Severity {
	case SeverityInfo, SeverityWarning, SeverityError:
	default:
		errs = append(errs, fmt.Errorf("unknown severity %q", r.Severity))
	}
	if r.HoldSeconds < 0 || r.ClearHoldSeconds < 0 {
		errs = append(errs, errors.New("hold seconds must not be negative"))
	}
	if k := r.MatchKind; k != "" && !vocab.ElementKind(k) && !vocab.ReservedElementKind(k) {
		errs = append(errs, fmt.Errorf("unknown kind %q", k))
	}
	if !vocab.Transport(r.MatchTransport) {
		errs = append(errs, fmt.Errorf("unknown transport %q", r.MatchTransport))
	}
	value, err := compileCond(r.Fact, r.Op, r.Value, types)
	if err != nil {
		errs = append(errs, err)
	}
	c := &rule{Rule: r, value: value}
	if r.When != nil {
		wv, err := compileCond(r.When.Fact, r.When.Op, r.When.Value, types)
		if err != nil {
			errs = append(errs, fmt.Errorf("when: %w", err))
		}
		c.when = &cond{fact: r.When.Fact, op: r.When.Op, value: wv}
	}
	if len(r.ClearValue) > 0 {
		clear, err := compileClear(r.Op, value, r.ClearValue)
		if err != nil {
			errs = append(errs, err)
		}
		c.clear = clear
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%w %q: %w", ErrInvalidRule, r.Name, err)
	}
	return c, nil
}

// compileCond prüft fact <op> value: Fakt bekannt, Operator passt zum Typ, Wert passt zum Fakt.
func compileCond(fact string, op Op, raw json.RawMessage, types FactTypes) (any, error) {
	t, ok := types(fact)
	if !ok {
		return nil, fmt.Errorf("unknown fact %q", fact)
	}
	if op.Direction() == "" {
		return nil, fmt.Errorf("unknown operator %q", op)
	}
	v, err := parseValue(raw)
	if err != nil {
		return nil, err
	}
	switch op {
	case OpIn, OpNotIn:
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			return nil, fmt.Errorf("%s needs a non-empty list", op)
		}
		for _, item := range list {
			if !valueFits(item, t) {
				return nil, fmt.Errorf("%v does not fit %s fact %q", item, t, fact)
			}
		}
	case OpLt, OpLte, OpGt, OpGte:
		if _, ok := v.(num); !ok || (t != vocab.Int && t != vocab.Float) {
			return nil, fmt.Errorf("%s needs a number and a numeric fact, %q is %s", op, fact, t)
		}
	default:
		if !valueFits(v, t) {
			return nil, fmt.Errorf("value %s does not fit %s fact %q", raw, t, fact)
		}
	}
	return v, nil
}

// compileClear prüft die Hysterese: nur bei Ober- und Untergrenzen, auf der guten Seite der
// Grenze (lte 80 mit clear 75 endet erst bei 75).
func compileClear(op Op, value any, raw json.RawMessage) (any, error) {
	dir := op.Direction()
	if dir != DirectionUpper && dir != DirectionLower {
		return nil, fmt.Errorf("clear value needs an upper or lower bound, not %s", op)
	}
	v, err := parseValue(raw)
	if err != nil {
		return nil, fmt.Errorf("clear value: %w", err)
	}
	c, ok := v.(num)
	limit, okLimit := value.(num)
	switch {
	case !ok:
		return nil, errors.New("clear value must be a number")
	case okLimit && dir == DirectionUpper && c.cmp(limit) > 0,
		okLimit && dir == DirectionLower && c.cmp(limit) < 0:
		return nil, fmt.Errorf("clear value %s is on the wrong side of %s", raw, op)
	}
	return c, nil
}

// violated meldet, ob die Regel verletzt ist. Ist active gesetzt (ein Befund der Regel ist
// aktiv), gilt die Hysterese. Ein unerfülltes when erfüllt die Regel. ok ist false, wenn ein
// Fakt fehlt oder sein Typ nicht passt.
func (r *rule) violated(fact func(string) (any, bool), active bool) (violated, ok bool) {
	if r.when != nil {
		w, ok := fact(r.when.fact)
		if !ok {
			return false, false
		}
		h, ok := holds(r.when.op, w, r.when.value)
		if !ok || !h {
			return false, ok
		}
	}
	v, ok := fact(r.Fact)
	if !ok {
		return false, false
	}
	limit := r.value
	if active && r.clear != nil {
		limit = r.clear
	}
	h, ok := holds(r.Op, v, limit)
	return ok && !h, ok
}

// stage ist die Stufe einer Regel an einem Element; kleiner ist spezifischer: erst der
// Geltungsbereich (Element und Vorfahren, dann Gruppen, dann global), dann der Abstand, dann
// mehr Filter.
type stage struct {
	class, distance, filters int
}

func (a stage) cmp(b stage) int {
	return cmp.Or(cmp.Compare(a.class, b.class), cmp.Compare(a.distance, b.distance), cmp.Compare(b.filters, a.filters))
}

// target ist ein Element mit allem, was die Auswahl seiner Regeln braucht.
type target struct {
	g      *inventory.Graph
	e      inventory.Element
	driver string
	scopes map[uuid.UUID]int
	facts  sdk.Facts
}

// newTarget liefert das Ziel, wenn das Element bekannt und überwacht ist.
func newTarget(s State, id uuid.UUID) (target, bool) {
	g := s.Graph()
	e, ok := g.Element(id)
	if !ok || !g.Monitored(id) {
		return target{}, false
	}
	src, _ := g.Source(e.SourceID)
	return target{g: g, e: e, driver: src.Driver, scopes: g.Scopes(id), facts: s.Facts(id)}, true
}

// fact liefert einen Fakt des Elements; present kommt aus dem Inventar. Ein verschwundenes
// Element hat nur present, seine übrigen Fakten sind entfallen.
func (t target) fact(key string) (any, bool) {
	if key == vocab.Present {
		return t.e.Present, true
	}
	if !t.e.Present {
		return nil, false
	}
	v, ok := t.facts[key]
	return v, ok
}

// stageAt liefert die Stufe der Regel am Element, wenn sie dort gilt: aktiviert, Geltungsbereich
// und Filter passen, Fakt (und bei when auch dessen Fakt) vorhanden.
func (r *rule) stageAt(t target) (stage, bool) {
	var s stage
	if !r.Enabled {
		return s, false
	}
	if r.ScopeElementID == uuid.Nil {
		s.class = 2
	} else {
		d, ok := t.scopes[r.ScopeElementID]
		if !ok {
			return s, false
		}
		s.distance = d
		if scope, _ := t.g.Element(r.ScopeElementID); scope.Kind == inventory.KindGroup {
			s.class = 1
		}
	}
	for _, f := range []struct{ want, got string }{
		{r.MatchKind, string(t.e.Kind)},
		{r.MatchTransport, t.e.Transport},
		{r.MatchDriver, t.driver},
	} {
		if f.want == "" {
			continue
		}
		if f.want != f.got {
			return s, false
		}
		s.filters++
	}
	if _, ok := t.fact(r.Fact); !ok {
		return s, false
	}
	if r.when != nil {
		if _, ok := t.fact(r.when.fact); !ok {
			return s, false
		}
	}
	return s, true
}

// RuleSet sind die Regeln an einem Element für einen Fakt und eine Richtung: Rules gelten (die
// gewinnende Stufe, alle ihre Regeln), Overridden sind von ihr überschrieben.
type RuleSet struct {
	Fact       string
	Direction  Direction
	Rules      []Rule
	Overridden []Rule
}

type ruleSet struct {
	fact       string
	dir        Direction
	rules      []*rule
	overridden []*rule
}

// Rules ist ein unveränderlicher, geprüfter Satz von Regeln. Der Evaluator ersetzt ihn bei jeder
// Änderung; die View hält ihn.
type Rules struct {
	byID map[uuid.UUID]*rule
	list []*rule // nach Name, dann ID
}

// NewRules prüft die Regeln gegen den Fakt-Katalog. Ungültige (etwa weil ein Treiber nicht
// mehr registriert ist) bleiben draußen; ihre Fehler kommen zurück.
func NewRules(rules []Rule, types FactTypes) (*Rules, []error) {
	byID := make(map[uuid.UUID]*rule, len(rules))
	var errs []error
	for _, r := range rules {
		c, err := compileRule(r, types)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		byID[r.ID] = c
	}
	return newRules(byID), errs
}

func newRules(byID map[uuid.UUID]*rule) *Rules {
	list := slices.SortedFunc(maps.Values(byID), compareRules)
	return &Rules{byID: byID, list: list}
}

func compareRules(a, b *rule) int {
	return cmp.Or(strings.Compare(a.Name, b.Name), slices.Compare(a.ID[:], b.ID[:]))
}

// without liefert einen neuen Satz ohne die Regeln.
func (rs *Rules) without(ids ...uuid.UUID) *Rules {
	byID := maps.Clone(rs.byID)
	for _, id := range ids {
		delete(byID, id)
	}
	return newRules(byID)
}

// Rule liefert eine Regel, All alle nach Name.
func (rs *Rules) Rule(id uuid.UUID) (Rule, bool) {
	r, ok := rs.byID[id]
	if !ok {
		return Rule{}, false
	}
	return r.Rule, true
}

func (rs *Rules) All() iter.Seq[Rule] {
	return func(yield func(Rule) bool) {
		for _, r := range rs.list {
			if !yield(r.Rule) {
				return
			}
		}
	}
}

// sets liefert die Regeln an einem Element pro Fakt und Richtung, sortiert nach Fakt und
// Richtung. Nicht überwachte und unbekannte Elemente haben keine.
func (rs *Rules) sets(t target) []ruleSet {
	type key struct {
		fact string
		dir  Direction
	}
	type staged struct {
		r *rule
		s stage
	}
	byKey := map[key][]staged{}
	for _, r := range rs.list {
		if s, ok := r.stageAt(t); ok {
			k := key{r.Fact, r.Op.Direction()}
			byKey[k] = append(byKey[k], staged{r, s})
		}
	}
	sets := make([]ruleSet, 0, len(byKey))
	for k, rules := range byKey {
		best := slices.MinFunc(rules, func(a, b staged) int { return a.s.cmp(b.s) }).s
		set := ruleSet{fact: k.fact, dir: k.dir}
		for _, r := range rules {
			if r.s.cmp(best) == 0 {
				set.rules = append(set.rules, r.r)
			} else {
				set.overridden = append(set.overridden, r.r)
			}
		}
		sets = append(sets, set)
	}
	slices.SortFunc(sets, func(a, b ruleSet) int {
		return cmp.Or(strings.Compare(a.fact, b.fact), strings.Compare(string(a.dir), string(b.dir)))
	})
	return sets
}

// EffectiveRules liefert für die UI pro Fakt und Richtung, welche Regeln am Element gelten und
// welche sie überschreiben.
func (rs *Rules) EffectiveRules(s State, id uuid.UUID) []RuleSet {
	t, ok := newTarget(s, id)
	if !ok {
		return nil
	}
	plain := func(rules []*rule) []Rule {
		out := make([]Rule, len(rules))
		for i, r := range rules {
			out[i] = r.Rule
		}
		return out
	}
	var out []RuleSet
	for _, set := range rs.sets(t) {
		out = append(out, RuleSet{Fact: set.fact, Direction: set.dir, Rules: plain(set.rules), Overridden: plain(set.overridden)})
	}
	return out
}
