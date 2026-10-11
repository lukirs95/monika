// Package state hält den aktuellen Zustand im Speicher des Core: die Fakten aller Elemente und
// die Lebendigkeit der Quellen. Es ist Logik ohne eigene Goroutinen; Besitzer ist die Schleife
// des Core, deshalb gibt es keine Locks (docs/contracts/core.md).
package state

import (
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

// Changed nennt pro Element die Fakt-Schlüssel, die sich geändert haben, neu sind oder
// weggefallen sind.
type Changed map[uuid.UUID][]string

// Facts hält die aktuellen Fakten aller Elemente. Die Fakten eines Elements werden nie verändert,
// sondern ersetzt (copy-on-write); Snapshot und Element geben sie deshalb ohne Kopie weiter.
type Facts struct {
	byElement map[uuid.UUID]sdk.Facts
	source    map[uuid.UUID]uuid.UUID              // Element → Quelle
	bySource  map[uuid.UUID]map[uuid.UUID]struct{} // Quelle → Elemente mit Fakten
}

// NewFacts liefert einen leeren Speicher.
func NewFacts() *Facts {
	return &Facts{
		byElement: map[uuid.UUID]sdk.Facts{},
		source:    map[uuid.UUID]uuid.UUID{},
		bySource:  map[uuid.UUID]map[uuid.UUID]struct{}{},
	}
}

// Replace ersetzt alle Fakten der Quelle (vollständiger Snapshot): Was in facts fehlt, ist weg.
func (f *Facts) Replace(source uuid.UUID, facts map[uuid.UUID]sdk.Facts) Changed {
	changed := Changed{}
	for id := range f.bySource[source] {
		if _, ok := facts[id]; !ok {
			changed.add(id, f.remove(id))
		}
	}
	for id, next := range facts {
		changed.add(id, f.set(source, id, maps.Clone(next)))
	}
	return changed
}

// Merge übernimmt geänderte Fakten der Quelle (Update); fehlende Schlüssel bleiben.
func (f *Facts) Merge(source uuid.UUID, facts map[uuid.UUID]sdk.Facts) Changed {
	changed := Changed{}
	for id, delta := range facts {
		next := maps.Clone(f.byElement[id])
		if next == nil {
			next = sdk.Facts{}
		}
		maps.Copy(next, delta)
		changed.add(id, f.set(source, id, next))
	}
	return changed
}

// Drop entfernt die Fakten der Elemente, z. B. weil sie verschwunden sind.
func (f *Facts) Drop(ids []uuid.UUID) Changed {
	changed := Changed{}
	for _, id := range ids {
		changed.add(id, f.remove(id))
	}
	return changed
}

// DropSource entfernt alle Fakten der Quelle, z. B. weil sie gelöscht ist.
func (f *Facts) DropSource(source uuid.UUID) Changed {
	return f.Drop(slices.Collect(maps.Keys(f.bySource[source])))
}

// Get liefert einen Fakt eines Elements.
func (f *Facts) Get(id uuid.UUID, key string) (any, bool) {
	v, ok := f.byElement[id][key]
	return v, ok
}

// Element liefert die Fakten eines Elements; der Aufrufer darf sie nicht verändern.
func (f *Facts) Element(id uuid.UUID) sdk.Facts { return f.byElement[id] }

// Snapshot liefert die Fakten aller Elemente für die View. Die äußere Map ist eine Kopie, die
// Fakten der Elemente werden geteilt; der Aufrufer darf sie nicht verändern.
func (f *Facts) Snapshot() map[uuid.UUID]sdk.Facts { return maps.Clone(f.byElement) }

func (f *Facts) set(source, id uuid.UUID, next sdk.Facts) []string {
	keys := diff(f.byElement[id], next)
	if len(keys) == 0 {
		return nil
	}
	if old, ok := f.source[id]; ok && old != source {
		delete(f.bySource[old], id)
	}
	f.byElement[id] = next
	f.source[id] = source
	if f.bySource[source] == nil {
		f.bySource[source] = map[uuid.UUID]struct{}{}
	}
	f.bySource[source][id] = struct{}{}
	return keys
}

func (f *Facts) remove(id uuid.UUID) []string {
	keys := diff(f.byElement[id], nil)
	if src, ok := f.source[id]; ok {
		delete(f.bySource[src], id)
		if len(f.bySource[src]) == 0 {
			delete(f.bySource, src)
		}
	}
	delete(f.byElement, id)
	delete(f.source, id)
	return keys
}

func (c Changed) add(id uuid.UUID, keys []string) {
	if len(keys) > 0 {
		c[id] = keys
	}
}

// diff liefert sortiert die Schlüssel, die in genau einer der Maps stehen oder verschiedene
// Werte haben. Fakten sind bool, int64, float64 oder string und damit vergleichbar.
func diff(a, b sdk.Facts) []string {
	var keys []string
	for k, va := range a {
		if vb, ok := b[k]; !ok || va != vb {
			keys = append(keys, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}
