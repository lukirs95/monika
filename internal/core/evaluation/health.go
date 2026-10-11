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

// Health berechnet die Health der Elemente neu, dazu die aller Elemente mit geänderten Befunden.
// Rollups führt es inkrementell: Ändert sich die Zählung eines Elements, passt es die Zähler
// aller Geräte und Gruppen an, die es enthalten. Nach einer Änderung am Graph oder an den Regeln
// berechnet es alles neu. Es liefert, was sich geändert hat; geschrieben wird gesammelt in Tick.
func (e *Evaluator) Health(s State, elements []uuid.UUID) []ElementHealth {
	g := s.Graph()
	todo := e.healthDirty
	e.healthDirty = map[uuid.UUID]bool{}
	for _, id := range elements {
		todo[id] = true
	}
	next := e.health
	cloned := false
	scopes := map[uuid.UUID]bool{} // Geräte und Gruppen, deren Health neu zu bestimmen ist
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
		e.buckets, e.counts = map[uuid.UUID]bucket{}, map[uuid.UUID]*Rollup{}
		for id, el := range g.All() {
			todo[id] = true
			if isScope(el.Kind) {
				e.counts[id] = &Rollup{}
				scopes[id] = true
			}
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
	for _, id := range sortedIDs(todo) {
		el, ok := g.Element(id)
		switch {
		case !ok:
		case isScope(el.Kind):
			scopes[id] = true // etwa geändertes monitored des Geräts
		default:
			h := e.elementHealth(s, el)
			set(ElementHealth{ElementID: id, Health: h})
			e.count(g, el, h, scopes)
		}
	}
	for _, id := range sortedIDs(scopes) {
		r := *e.countsOf(id)
		set(ElementHealth{ElementID: id, Health: rollupHealth(r, g.Monitored(id)), Rollup: &r})
	}
	e.health = next
	return changed
}

// bucket ist, wie ein Element im Rollup zählt.
type bucket int

const (
	bucketNone bucket = iota // noch nicht gezählt
	bucketOK
	bucketWarning
	bucketError
	bucketUnknown
	bucketUnchecked
	bucketUnmonitored
	bucketMissing
)

func (r *Rollup) add(b bucket, n int) {
	switch b {
	case bucketOK:
		r.OK += n
	case bucketWarning:
		r.Warning += n
	case bucketError:
		r.Error += n
	case bucketUnknown:
		r.Unknown += n
	case bucketUnchecked:
		r.Unchecked += n
	case bucketUnmonitored:
		r.Unmonitored += n
	case bucketMissing:
		r.Missing += n
	}
}

// bucketOf: nicht überwacht; verschwunden zählt nur mit aktivem Befund (etwa aus present eq
// true) nach dessen Schwere; sonst nach Health, ohne Health als ungeprüft.
func (e *Evaluator) bucketOf(g *inventory.Graph, el inventory.Element, h Health) bucket {
	switch {
	case !g.Monitored(el.ID):
		return bucketUnmonitored
	case !el.Present:
		h = e.worst(el.ID, "")
		if h == HealthNone {
			return bucketMissing
		}
	}
	switch h {
	case HealthOK:
		return bucketOK
	case HealthWarning:
		return bucketWarning
	case HealthError:
		return bucketError
	case HealthUnknown:
		return bucketUnknown
	}
	return bucketUnchecked
}

// count führt die Zählung eines Elements nach und merkt sich die betroffenen Geräte und Gruppen:
// alle, die es enthalten, jedes genau einmal (Graph.Scopes).
func (e *Evaluator) count(g *inventory.Graph, el inventory.Element, h Health, scopes map[uuid.UUID]bool) {
	b := e.bucketOf(g, el, h)
	old := e.buckets[el.ID]
	if old == b {
		return
	}
	e.buckets[el.ID] = b
	for sc := range g.Scopes(el.ID) {
		if k := kindOf(g, sc); !isScope(k) {
			continue
		}
		r := e.countsOf(sc)
		r.add(old, -1)
		r.add(b, 1)
		scopes[sc] = true
	}
}

func (e *Evaluator) countsOf(id uuid.UUID) *Rollup {
	r := e.counts[id]
	if r == nil {
		r = &Rollup{}
		e.counts[id] = r
	}
	return r
}

// rollupHealth ist die Health eines Geräts bzw. einer Gruppe: error > warning > unknown > ok;
// stillgelegt oder ohne zählende Elemente keine.
func rollupHealth(r Rollup, monitored bool) Health {
	switch {
	case !monitored:
		return HealthNone
	case r.Error > 0:
		return HealthError
	case r.Warning > 0:
		return HealthWarning
	case r.Unknown > 0:
		return HealthUnknown
	case r.OK > 0:
		return HealthOK
	}
	return HealthNone
}

func isScope(k inventory.Kind) bool { return k == inventory.KindDevice || k == inventory.KindGroup }

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
