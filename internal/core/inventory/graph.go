package inventory

import (
	"iter"
	"maps"
	"slices"

	"github.com/google/uuid"
)

// Graph ist ein unveränderlicher Stand des Inventars mit Indizes für Baum, Mitgliedschaften und
// Links. Das Inventar baut ihn nach jeder Änderung neu; lesen dürfen beliebig viele Goroutinen.
// Die Reihenfolge der IDs in den Indizes ist die der IDs (UUIDv7: die des Anlegens).
type Graph struct {
	elements map[uuid.UUID]Element
	sources  map[uuid.UUID]Source
	links    map[uuid.UUID]Link

	children map[uuid.UUID][]uuid.UUID // Element → Kinder über parent_id
	out, in  map[uuid.UUID][]uuid.UUID // Element → Links (alle Typen)
	members  map[uuid.UUID][]uuid.UUID // Gruppe → Mitglieder
	groups   map[uuid.UUID][]uuid.UUID // Element → Gruppen, die es direkt enthalten
}

// newGraph baut die Indizes; die Maps gehören danach dem Graph.
func newGraph(elements map[uuid.UUID]Element, sources map[uuid.UUID]Source, links map[uuid.UUID]Link) *Graph {
	g := &Graph{
		elements: elements, sources: sources, links: links,
		children: map[uuid.UUID][]uuid.UUID{},
		out:      map[uuid.UUID][]uuid.UUID{},
		in:       map[uuid.UUID][]uuid.UUID{},
		members:  map[uuid.UUID][]uuid.UUID{},
		groups:   map[uuid.UUID][]uuid.UUID{},
	}
	for _, id := range sortedKeys(elements) {
		if p := elements[id].ParentID; p != uuid.Nil {
			g.children[p] = append(g.children[p], id)
		}
	}
	for _, id := range sortedKeys(links) {
		l := links[id]
		if l.FromID != uuid.Nil {
			g.out[l.FromID] = append(g.out[l.FromID], id)
		}
		if l.ToID != uuid.Nil {
			g.in[l.ToID] = append(g.in[l.ToID], id)
		}
		if l.Type == LinkMember {
			g.members[l.FromID] = append(g.members[l.FromID], l.ToID)
			g.groups[l.ToID] = append(g.groups[l.ToID], l.FromID)
		}
	}
	return g
}

func sortedKeys[V any](m map[uuid.UUID]V) []uuid.UUID {
	return slices.SortedFunc(maps.Keys(m), func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
}

// Graph liefert den aktuellen Stand; unverändert seit dem letzten Aufruf derselbe.
func (inv *Inventory) Graph() *Graph {
	if inv.graph == nil {
		inv.graph = newGraph(maps.Clone(inv.elements), maps.Clone(inv.sources), maps.Clone(inv.links))
	}
	return inv.graph
}

func (g *Graph) Element(id uuid.UUID) (Element, bool) {
	e, ok := g.elements[id]
	return e, ok
}

func (g *Graph) Source(id uuid.UUID) (Source, bool) {
	s, ok := g.sources[id]
	return s, ok
}

func (g *Graph) Link(id uuid.UUID) (Link, bool) {
	l, ok := g.links[id]
	return l, ok
}

// All liefert alle Elemente, Sources alle Quellen, Links alle Links, jeweils in beliebiger
// Reihenfolge.
func (g *Graph) All() iter.Seq2[uuid.UUID, Element]    { return maps.All(g.elements) }
func (g *Graph) Sources() iter.Seq2[uuid.UUID, Source] { return maps.All(g.sources) }
func (g *Graph) Links() iter.Seq2[uuid.UUID, Link]     { return maps.All(g.links) }

// Outgoing und Incoming liefern die Links aller Typen, die am Element beginnen bzw. enden.
func (g *Graph) Outgoing(id uuid.UUID) iter.Seq[Link] { return g.linkSeq(g.out[id]) }
func (g *Graph) Incoming(id uuid.UUID) iter.Seq[Link] { return g.linkSeq(g.in[id]) }

func (g *Graph) linkSeq(ids []uuid.UUID) iter.Seq[Link] {
	return func(yield func(Link) bool) {
		for _, id := range ids {
			if !yield(g.links[id]) {
				return
			}
		}
	}
}

// Children liefert die Kinder eines Elements über parent_id.
func (g *Graph) Children(id uuid.UUID) iter.Seq[uuid.UUID] { return slices.Values(g.children[id]) }

// Ancestors liefert die Vorfahren über parent_id, der nächste zuerst, das Gerät zuletzt.
func (g *Graph) Ancestors(id uuid.UUID) iter.Seq[uuid.UUID] {
	return func(yield func(uuid.UUID) bool) {
		seen := map[uuid.UUID]bool{id: true}
		for p := g.elements[id].ParentID; p != uuid.Nil && !seen[p]; p = g.elements[p].ParentID {
			if _, ok := g.elements[p]; !ok || !yield(p) {
				return
			}
			seen[p] = true
		}
	}
}

// Scopes liefert alle Geltungsbereiche, die das Element umfassen, mit ihrem Abstand: das Element
// selbst (0), seine Vorfahren und die Gruppen, die es oder einen Vorfahren rekursiv enthalten.
// Der Abstand zählt Schritte über Eltern und Mitgliedschaften zusammen, der kürzeste Weg zählt.
func (g *Graph) Scopes(id uuid.UUID) map[uuid.UUID]int {
	if _, ok := g.elements[id]; !ok {
		return nil
	}
	dist := map[uuid.UUID]int{id: 0}
	for queue := []uuid.UUID{id}; len(queue) > 0; queue = queue[1:] {
		cur := queue[0]
		visit := func(n uuid.UUID) {
			if _, seen := dist[n]; !seen {
				dist[n] = dist[cur] + 1
				queue = append(queue, n)
			}
		}
		if p := g.elements[cur].ParentID; p != uuid.Nil {
			if _, ok := g.elements[p]; ok {
				visit(p)
			}
		}
		for _, grp := range g.groups[cur] {
			visit(grp)
		}
	}
	return dist
}

// InScope ist die Auflösung von Geltungsbereichen für Regeln, Rechte, Weiterleitung, Verlauf und
// Watch: uuid.Nil = alles; ein Element umfasst sich und seine Nachfahren; eine Gruppe ihre
// Mitglieder rekursiv, jeweils mit Nachfahren.
func (g *Graph) InScope(scope, id uuid.UUID) bool {
	switch {
	case scope == uuid.Nil:
		return true
	case scope == id:
		_, ok := g.elements[id]
		return ok
	case g.elements[scope].Kind == KindGroup:
		_, ok := g.Scopes(id)[scope]
		return ok
	}
	for a := range g.Ancestors(id) {
		if a == scope {
			return true
		}
	}
	return false
}

// ScopeElements liefert alle Elemente im Geltungsbereich, jedes genau einmal, beginnend mit dem
// Geltungsbereich selbst; uuid.Nil = alle Elemente.
func (g *Graph) ScopeElements(scope uuid.UUID) iter.Seq[uuid.UUID] {
	if scope == uuid.Nil {
		return maps.Keys(g.elements)
	}
	return func(yield func(uuid.UUID) bool) {
		if _, ok := g.elements[scope]; !ok {
			return
		}
		seen := map[uuid.UUID]bool{scope: true}
		for queue := []uuid.UUID{scope}; len(queue) > 0; queue = queue[1:] {
			cur := queue[0]
			if !yield(cur) {
				return
			}
			for _, next := range [][]uuid.UUID{g.children[cur], g.members[cur]} {
				for _, n := range next {
					if _, ok := g.elements[n]; ok && !seen[n] {
						seen[n] = true
						queue = append(queue, n)
					}
				}
			}
		}
	}
}

// Monitored ist das effektive monitored: Das Element und alle Vorfahren sind überwacht, und seine
// Quelle ist aktiviert. Gruppen vererben nichts an ihre Mitglieder.
func (g *Graph) Monitored(id uuid.UUID) bool {
	e, ok := g.elements[id]
	if !ok || !e.Monitored {
		return false
	}
	if e.SourceID != uuid.Nil && !g.sources[e.SourceID].Enabled {
		return false
	}
	for a := range g.Ancestors(id) {
		if !g.elements[a].Monitored {
			return false
		}
	}
	return true
}

// Path ist der Anzeigename eines Elements wie bei Inventory.Path.
func (g *Graph) Path(id uuid.UUID) string { return path(g.elements, id) }
