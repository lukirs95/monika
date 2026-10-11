// Package evaluation bewertet den Zustand: Regeln, Befunde, Ursache und Symptom, Health und Rollup.
package evaluation

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
)

// Health ist der bewertete Zustand eines Elements.
type Health string

const (
	// HealthNone: keine Health, weil das Element nicht überwacht, verschwunden oder ungeprüft
	// ist; bei Gerät und Gruppe auch, wenn es nichts zusammenzufassen gibt.
	HealthNone    Health = ""
	HealthOK      Health = "ok"
	HealthWarning Health = "warning"
	HealthError   Health = "error"
	HealthUnknown Health = "unknown" // die Quelle ist nicht Live
)

// Rollup zählt die verschiedenen Elemente im Geltungsbereich eines Geräts oder einer Gruppe nach
// Health, jedes genau einmal. Unchecked, Unmonitored und Missing sind informativ und fließen
// nicht in die Health ein.
type Rollup struct {
	OK          int `json:"ok"`
	Warning     int `json:"warning"`
	Error       int `json:"error"`
	Unknown     int `json:"unknown"`
	Unchecked   int `json:"unchecked"` // ohne geltende Regel
	Unmonitored int `json:"unmonitored"`
	Missing     int `json:"missing"`
}

// ElementHealth ist die gespeicherte Bewertung eines Elements.
type ElementHealth struct {
	ElementID uuid.UUID
	Health    Health
	Rollup    *Rollup // nur bei Geräten und Gruppen
}

// Healths liefert Health und Rollup aller bewerteten Elemente. Die Map wird nie verändert, nur
// ersetzt (copy-on-write); die View hält sie, Aufrufer schreiben nicht hinein.
func (e *Evaluator) Healths() map[uuid.UUID]ElementHealth { return e.health }

// Health berechnet die Health der Elemente neu, dazu die aller Elemente mit geänderten Befunden
// und danach die Rollups ihrer Geräte und Gruppen. Nach einer Änderung am Graph oder an den
// Regeln berechnet es alles neu. Es liefert, was sich geändert hat; geschrieben wird gesammelt
// in Tick.
func (e *Evaluator) Health(s State, elements []uuid.UUID) []ElementHealth {
	g := s.Graph()
	todo := e.healthDirty
	e.healthDirty = map[uuid.UUID]bool{}
	for _, id := range elements {
		todo[id] = true
	}
	next := e.health
	cloned := false
	if g != e.healthGraph || e.rules != e.healthRules {
		e.healthGraph, e.healthRules = g, e.rules
		next, cloned = map[uuid.UUID]ElementHealth{}, true
		for id, h := range e.health {
			if _, ok := g.Element(id); ok {
				next[id] = h
			}
		}
		for id := range e.unsaved {
			if _, ok := g.Element(id); !ok {
				delete(e.unsaved, id) // die Datenbank hat die Zeile per CASCADE gelöscht
			}
		}
		for id := range g.All() {
			todo[id] = true
		}
	}

	var changed []ElementHealth
	set := func(h ElementHealth) {
		if old, ok := next[h.ElementID]; ok && sameHealth(old, h) {
			return
		}
		if !cloned {
			next, cloned = maps.Clone(next), true
		}
		next[h.ElementID] = h
		e.unsaved[h.ElementID] = h
		changed = append(changed, h)
	}
	scopes := map[uuid.UUID]bool{}
	for _, id := range sortedIDs(todo) {
		el, ok := g.Element(id)
		switch {
		case !ok:
		case el.Kind == inventory.KindDevice || el.Kind == inventory.KindGroup:
			scopes[id] = true
		default:
			set(ElementHealth{ElementID: id, Health: e.elementHealth(s, el)})
			for sc := range g.Scopes(id) {
				if k := kindOf(g, sc); k == inventory.KindDevice || k == inventory.KindGroup {
					scopes[sc] = true
				}
			}
		}
	}
	e.health = next // Rollups lesen die neuen Werte der Elemente
	for _, id := range sortedIDs(scopes) {
		h, r := e.rollup(s, id)
		set(ElementHealth{ElementID: id, Health: h, Rollup: &r})
	}
	e.health = next
	return changed
}

// elementHealth ist die Health eines Elements (docs/ARCHITECTURE.md, Abschnitt 8).
func (e *Evaluator) elementHealth(s State, el inventory.Element) Health {
	g := s.Graph()
	if !g.Monitored(el.ID) || !el.Present {
		return HealthNone
	}
	if el.SourceID != uuid.Nil && !s.Live(el.SourceID) {
		// Der Zugang zeigt seine Systembefunde, alles andere ist unbekannt.
		if w := e.worst(el.ID, OriginSystem); el.Kind == inventory.KindAccess && w != HealthNone {
			return w
		}
		return HealthUnknown
	}
	if w := e.worst(el.ID, ""); w != HealthNone {
		return w
	}
	if el.Kind == inventory.KindAccess && s.Heartbeat(el.SourceID) > 0 {
		return HealthOK // die Lebendigkeit der Quelle ist eine Prüfung
	}
	if t, ok := newTarget(s, el.ID); ok && len(e.rules.sets(t)) > 0 {
		return HealthOK
	}
	return HealthNone // ungeprüft
}

// worst liefert den schlimmsten aktiven Befund mit warning oder error am Element, nur einer
// Herkunft ("" = alle).
func (e *Evaluator) worst(id uuid.UUID, origin Origin) Health {
	h := HealthNone
	for _, tr := range e.tracks[id] {
		if !tr.isActive() || (origin != "" && tr.finding.Origin != origin) {
			continue
		}
		switch tr.finding.Severity {
		case SeverityError:
			return HealthError
		case SeverityWarning:
			h = HealthWarning
		}
	}
	return h
}

// rollup fasst die verschiedenen Elemente im Geltungsbereich eines Geräts bzw. einer Gruppe
// zusammen, ohne Gerät- und Gruppen-Elemente, jedes genau einmal: error > warning > unknown > ok.
func (e *Evaluator) rollup(s State, scope uuid.UUID) (Health, Rollup) {
	g := s.Graph()
	var r Rollup
	for id := range g.ScopeElements(scope) {
		el, _ := g.Element(id)
		if el.Kind == inventory.KindDevice || el.Kind == inventory.KindGroup {
			continue
		}
		switch {
		case !g.Monitored(id):
			r.Unmonitored++
		case !el.Present:
			// Verschwunden zählt nur mit aktivem Befund, etwa aus present eq true.
			switch e.worst(id, "") {
			case HealthError:
				r.Error++
			case HealthWarning:
				r.Warning++
			default:
				r.Missing++
			}
		default:
			h, ok := e.health[id]
			if !ok {
				h = ElementHealth{ElementID: id, Health: e.elementHealth(s, el)}
			}
			switch h.Health {
			case HealthOK:
				r.OK++
			case HealthWarning:
				r.Warning++
			case HealthError:
				r.Error++
			case HealthUnknown:
				r.Unknown++
			default:
				r.Unchecked++
			}
		}
	}
	switch {
	case !g.Monitored(scope): // stillgelegt
		return HealthNone, r
	case r.Error > 0:
		return HealthError, r
	case r.Warning > 0:
		return HealthWarning, r
	case r.Unknown > 0:
		return HealthUnknown, r
	case r.OK > 0:
		return HealthOK, r
	}
	return HealthNone, r
}

func kindOf(g *inventory.Graph, id uuid.UUID) inventory.Kind {
	el, _ := g.Element(id)
	return el.Kind
}

func sameHealth(a, b ElementHealth) bool {
	if a.Health != b.Health || (a.Rollup == nil) != (b.Rollup == nil) {
		return false
	}
	return a.Rollup == nil || *a.Rollup == *b.Rollup
}

// saveHealth schreibt die geänderte Health gesammelt; scheitert das, bleibt sie offen.
func (e *Evaluator) saveHealth(ctx context.Context, res *result) {
	if len(e.unsaved) == 0 {
		return
	}
	batch := slices.SortedFunc(maps.Values(e.unsaved), func(a, b ElementHealth) int {
		return slices.Compare(a.ElementID[:], b.ElementID[:])
	})
	if err := e.write(ctx, func(ctx context.Context) error { return e.store.SaveHealth(ctx, batch) }); err != nil {
		res.errs = append(res.errs, fmt.Errorf("save health of %d elements: %w", len(batch), err))
		return
	}
	clear(e.unsaved)
}
