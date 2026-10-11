package evaluation

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

// Alarms übernimmt die Gerätealarme einer Quelle: replace (Snapshot) ersetzt alle Alarme der
// Quelle, sonst (Update) nur die der genannten Elemente; eine leere Liste löscht sie. Ein neuer
// Alarm erzeugt sofort einen Befund, ein fehlender beendet ihn als behoben.
func (e *Evaluator) Alarms(ctx context.Context, now time.Time, sourceID uuid.UUID, alarms map[uuid.UUID][]sdk.Alarm, replace bool, s State) ([]FindingChange, error) {
	var res result
	g := s.Graph()
	e.prune(g, &res)
	touched := map[uuid.UUID]bool{}
	if replace {
		for id := range e.alarms {
			if el, _ := g.Element(id); el.SourceID == sourceID {
				delete(e.alarms, id)
				touched[id] = true
			}
		}
		for id, m := range e.tracks {
			el, _ := g.Element(id)
			for _, tr := range m {
				if tr.finding.Origin == OriginDevice && el.SourceID == sourceID {
					touched[id] = true
				}
			}
		}
	}
	for id, list := range alarms {
		if len(list) == 0 {
			delete(e.alarms, id)
		} else {
			e.alarms[id] = slices.Clone(list)
		}
		touched[id] = true
	}
	for _, id := range sortedIDs(touched) {
		e.reconcile(ctx, now, id, s, OriginDevice, &res)
	}
	return e.finish(ctx, g, &res)
}

// Discovery übernimmt das Ergebnis eines Versuchs, einen Snapshot der Quelle zu speichern: Ein
// Fehler erzeugt source.discovery_failed am Zugang, ein Erfolg beendet ihn als behoben.
func (e *Evaluator) Discovery(ctx context.Context, now time.Time, sourceID uuid.UUID, err error, s State) ([]FindingChange, error) {
	var res result
	g := s.Graph()
	e.prune(g, &res)
	if err != nil {
		e.discoveryErr[sourceID] = err.Error()
	} else {
		delete(e.discoveryErr, sourceID)
	}
	if src, ok := g.Source(sourceID); ok {
		e.reconcile(ctx, now, src.AccessElementID, s, OriginSystem, &res)
	}
	return e.finish(ctx, g, &res)
}

// CreateRule prüft und speichert eine neue Regel und bewertet ihren Geltungsbereich neu.
func (e *Evaluator) CreateRule(ctx context.Context, now time.Time, actor uuid.UUID, r Rule, s State) ([]FindingChange, error) {
	c, err := e.checkRule(r, s)
	if err != nil {
		return nil, err
	}
	if err := e.write(ctx, func(ctx context.Context) error { return e.store.CreateRule(ctx, actor, r) }); err != nil {
		return nil, fmt.Errorf("create rule %q: %w", r.Name, err)
	}
	e.rules = e.rules.with(c)
	return e.reevaluate(ctx, now, s, r.ID, r.ScopeElementID)
}

// UpdateRule prüft und speichert eine geänderte Regel und bewertet den alten und den neuen
// Geltungsbereich neu. Wird sie deaktiviert, hat der Store ihre Befunde schon zurückgezogen.
// Auch eine Regel, die beim Laden nicht mehr zum Katalog passte, lässt sich so reparieren.
func (e *Evaluator) UpdateRule(ctx context.Context, now time.Time, actor uuid.UUID, r Rule, s State) ([]FindingChange, error) {
	c, err := e.checkRule(r, s)
	if err != nil {
		return nil, err
	}
	if err := e.write(ctx, func(ctx context.Context) error { return e.store.UpdateRule(ctx, actor, r) }); err != nil {
		return nil, fmt.Errorf("update rule %q: %w", r.Name, err)
	}
	scopes := []uuid.UUID{r.ScopeElementID}
	if old, ok := e.rules.byID[r.ID]; ok {
		scopes = append(scopes, old.ScopeElementID)
	}
	e.rules = e.rules.with(c)
	return e.reevaluate(ctx, now, s, r.ID, scopes...)
}

// DeleteRule löscht eine Regel; ihre aktiven Befunde hat der Store schon zurückgezogen. Ihr
// Geltungsbereich wird neu bewertet, denn von ihr überschriebene Regeln gelten wieder.
func (e *Evaluator) DeleteRule(ctx context.Context, now time.Time, actor uuid.UUID, id uuid.UUID, s State) ([]FindingChange, error) {
	if err := e.write(ctx, func(ctx context.Context) error { return e.store.DeleteRule(ctx, actor, id) }); err != nil {
		return nil, fmt.Errorf("delete rule %s: %w", id, err)
	}
	var scopes []uuid.UUID
	if old, ok := e.rules.byID[id]; ok {
		scopes = append(scopes, old.ScopeElementID)
		e.rules = e.rules.without(id)
	}
	return e.reevaluate(ctx, now, s, id, scopes...)
}

// AckFinding quittiert einen aktiven Befund; die erste Quittierung bleibt bestehen.
func (e *Evaluator) AckFinding(ctx context.Context, now time.Time, actor uuid.UUID, id uuid.UUID) (Finding, error) {
	f, ok := e.active[id]
	if !ok {
		return Finding{}, fmt.Errorf("ack finding %s: %w", id, ErrNotFound)
	}
	if err := e.write(ctx, func(ctx context.Context) error { return e.store.AckFinding(ctx, actor, id) }); err != nil {
		return Finding{}, fmt.Errorf("ack finding %s: %w", id, err)
	}
	if f.AckedAt.IsZero() {
		f.AckedAt, f.AckedByID = now, actor
		e.active[id] = f
		if tr := e.track(keyOf(f)); tr != nil {
			tr.finding = f
		}
	}
	return f, nil
}

// checkRule prüft eine Regel gegen den Katalog und den Zustand: ID gesetzt, Geltungsbereich
// existiert, match_driver registriert.
func (e *Evaluator) checkRule(r Rule, s State) (*rule, error) {
	c, err := compileRule(r, e.types)
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	if r.ID == uuid.Nil {
		errs = append(errs, fmt.Errorf("%w: missing ID", ErrInvalidRule))
	}
	if _, ok := s.Graph().Element(r.ScopeElementID); r.ScopeElementID != uuid.Nil && !ok {
		errs = append(errs, fmt.Errorf("%w: scope element %s not found", ErrInvalidRule, r.ScopeElementID))
	}
	if r.MatchDriver != "" && !e.drivers(r.MatchDriver) {
		errs = append(errs, fmt.Errorf("%w: driver %q is not registered", ErrInvalidRule, r.MatchDriver))
	}
	return c, errors.Join(errs...)
}

// reevaluate bewertet nach einer Regeländerung die Regelbefunde aller Elemente in den
// Geltungsbereichen neu (uuid.Nil = alle) und dazu jedes Element mit einem Befund der Regel.
func (e *Evaluator) reevaluate(ctx context.Context, now time.Time, s State, ruleID uuid.UUID, scopes ...uuid.UUID) ([]FindingChange, error) {
	var res result
	g := s.Graph()
	e.prune(g, &res)
	ids := map[uuid.UUID]bool{}
	for _, scope := range scopes {
		for id := range g.ScopeElements(scope) {
			ids[id] = true
		}
	}
	for id, m := range e.tracks {
		for key := range m {
			if key.rule == ruleID {
				ids[id] = true
			}
		}
	}
	for _, id := range sortedIDs(ids) {
		e.reconcile(ctx, now, id, s, OriginRule, &res)
	}
	return e.finish(ctx, g, &res)
}

func sortedIDs(set map[uuid.UUID]bool) []uuid.UUID {
	return slices.SortedFunc(maps.Keys(set), func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
}
