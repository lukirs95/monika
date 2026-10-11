package evaluation

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
)

// causeCalc leitet Ursache und Symptom über den Link-Graphen ab (docs/ARCHITECTURE.md,
// Abschnitt 8). Ein Problem breitet sich aus: vom Eltern-Element zu den Kindern, bei feeds A→B
// von A nach B, bei uses A→B von B nach A; member und represents tragen nichts.
type causeCalc struct {
	g         *inventory.Graph
	byElement map[uuid.UUID][]Finding             // nur warning und error: nur sie können Ursache sein
	up        map[uuid.UUID]map[uuid.UUID]Finding // Befund → mögliche Ursachen flussaufwärts
}

func newCauseCalc(g *inventory.Graph, active map[uuid.UUID]Finding) *causeCalc {
	c := &causeCalc{g: g, byElement: map[uuid.UUID][]Finding{}, up: map[uuid.UUID]map[uuid.UUID]Finding{}}
	for _, f := range active {
		if f.Severity == SeverityWarning || f.Severity == SeverityError {
			c.byElement[f.ElementID] = append(c.byElement[f.ElementID], f)
		}
	}
	return c
}

// upstream ruft yield für die Elemente auf, von denen sich ein Problem direkt auf id ausbreitet.
func (c *causeCalc) upstream(id uuid.UUID, yield func(uuid.UUID)) {
	if el, ok := c.g.Element(id); ok && el.ParentID != uuid.Nil {
		yield(el.ParentID)
	}
	for l := range c.g.Incoming(id) {
		if l.Type == inventory.LinkFeeds && l.FromID != uuid.Nil {
			yield(l.FromID)
		}
	}
	for l := range c.g.Outgoing(id) {
		if l.Type == inventory.LinkUses && l.ToID != uuid.Nil {
			yield(l.ToID)
		}
	}
}

// candidates liefert die aktiven Befunde mit warning oder error auf Elementen flussaufwärts von
// f, auch über mehrere Schritte. Das eigene Element zählt nur, wenn ein Kreis zu ihm zurückführt.
func (c *causeCalc) candidates(f Finding) map[uuid.UUID]Finding {
	if up, ok := c.up[f.ID]; ok {
		return up
	}
	up := map[uuid.UUID]Finding{}
	visited := map[uuid.UUID]bool{}
	var queue []uuid.UUID
	push := func(id uuid.UUID) { queue = append(queue, id) }
	c.upstream(f.ElementID, push)
	for ; len(queue) > 0; queue = queue[1:] {
		id := queue[0]
		if visited[id] {
			continue
		}
		visited[id] = true
		for _, g := range c.byElement[id] {
			if g.ID != f.ID {
				up[g.ID] = g
			}
		}
		c.upstream(id, push)
	}
	c.up[f.ID] = up
	return up
}

// symptom meldet, ob f ein Symptom ist: Flussaufwärts liegt ein möglicher Verursacher, der nicht
// im selben Kreis liegt und jünger ist (im Kreis ist der älteste die Wurzel).
func (c *causeCalc) symptom(f Finding) bool {
	for _, g := range c.candidates(f) {
		if _, mutual := c.candidates(g)[f.ID]; !mutual || !older(f, g) {
			return true
		}
	}
	return false
}

// cause liefert die Wurzel von f oder uuid.Nil, wenn f selbst Ursache ist. Unter mehreren Wurzeln
// gewinnt die schwerere, dann die ältere.
func (c *causeCalc) cause(f Finding) uuid.UUID {
	if !c.symptom(f) {
		return uuid.Nil
	}
	var roots, all []Finding
	for _, g := range c.candidates(f) {
		all = append(all, g)
		if !c.symptom(g) {
			roots = append(roots, g)
		}
	}
	if len(roots) == 0 { // kann nur bei widersprüchlichen Daten passieren
		roots = all
	}
	return slices.MinFunc(roots, betterRoot).ID
}

func older(a, b Finding) bool {
	return cmp.Or(a.RaisedAt.Compare(b.RaisedAt), slices.Compare(a.ID[:], b.ID[:])) < 0
}

func betterRoot(a, b Finding) int {
	rank := func(s Severity) int {
		if s == SeverityError {
			return 0
		}
		return 1
	}
	if r := cmp.Compare(rank(a.Severity), rank(b.Severity)); r != 0 {
		return r
	}
	if older(a, b) {
		return -1
	}
	return 1
}

// causeOf liefert die Ursache eines neuen Befunds zum Zeitpunkt seines Entstehens.
func (e *Evaluator) causeOf(g *inventory.Graph, f Finding) uuid.UUID {
	active := maps.Clone(e.active)
	active[f.ID] = f
	return newCauseCalc(g, active).cause(f)
}

// finish ordnet nach einer Verarbeitung Ursache und Symptom neu zu, wenn Befunde entstanden oder
// geendet haben, sich der Graph geändert hat oder ein früheres Schreiben gescheitert ist.
func (e *Evaluator) finish(ctx context.Context, g *inventory.Graph, res *result) ([]FindingChange, error) {
	changed := e.causeDirty || g != e.causeGraph
	for _, c := range res.changes {
		changed = changed || c.Raised != nil || c.Ended != uuid.Nil
	}
	if changed {
		e.assignCauses(ctx, g, res)
	}
	return res.done()
}

// assignCauses schreibt jede geänderte Zuordnung (SetFindingCause) und meldet sie als
// FindingChange mit CauseSet.
func (e *Evaluator) assignCauses(ctx context.Context, g *inventory.Graph, res *result) {
	e.causeGraph, e.causeDirty = g, false
	calc := newCauseCalc(g, e.active)
	for _, id := range slices.SortedFunc(maps.Keys(e.active), func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) }) {
		f := e.active[id]
		cause := calc.cause(f)
		if cause == f.CausedByID {
			continue
		}
		err := e.write(ctx, func(ctx context.Context) error { return e.store.SetFindingCause(ctx, id, cause) })
		if err != nil && !errors.Is(err, ErrNotFound) {
			e.causeDirty = true
			res.errs = append(res.errs, fmt.Errorf("set cause of %s on %q: %w", f.Code, f.ElementPath, err))
			continue
		}
		f.CausedByID = cause
		e.active[id] = f
		if tr := e.track(keyOf(f)); tr != nil {
			tr.finding = f
		}
		res.changes = append(res.changes, FindingChange{CauseSet: id, CausedBy: cause})
	}
}
