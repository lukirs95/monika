package api

import (
	"cmp"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core"
)

// Warteschlange eines Ereignisstroms (api.md, Verteiler). Ist der Strom frei, holt er jeden
// Eintrag sofort ab; während er schreibt, sammeln sich die folgenden nach Schlüssel. Ein
// Eintrag beschreibt, was sich an einem Objekt geändert hat; die Nutzdaten baut der Strom beim
// Schreiben aus der dann aktuellen View (V1).

type kind uint8

const (
	kindElements kind = iota // ein Eintrag pro Strom: vereinigte Element-IDs
	kindHealth               // ein Eintrag pro Strom: vereinigte Element-IDs
	kindFacts                // pro Element: vereinigte Fakt-Schlüssel
	kindFinding              // pro Befund
	kindRun                  // pro Lauf
	kindSource               // pro Quelle
)

type key struct {
	kind kind
	id   uuid.UUID
}

// item ist ein Eintrag der Warteschlange.
type item struct {
	key     key
	version uint64 // Version des neuesten Ereignisses darin
	seq     uint64 // Reihenfolge bei gleicher Version

	ids     map[uuid.UUID]bool // Elemente, Health: geänderte Elemente
	deleted map[uuid.UUID]bool // Elemente: gelöschte
	links   bool               // Elemente: Links geändert
	facts   map[string]bool    // Fakten: geänderte Schlüssel
	finding findingItem
	run     action.Run        // der neueste Stand des Laufs
	source  core.SourceStatus // der neueste Zustand der Quelle
}

// findingItem fasst die Ereignisse eines Befunds zusammen. Raised und Acked tragen den ganzen
// Befund; den liest der Strom aus der View. Ist er beendet, zählt nur noch ended.
type findingItem struct {
	raised, acked, cause bool
	ended                *core.FindingEnded
}

// queue ist die Warteschlange eines Stroms; der Verteiler schreibt hinein, der Strom holt ab.
type queue struct {
	mu     sync.Mutex
	items  map[key]*item
	seq    uint64
	resync bool          // alles Gesammelte verwerfen, der Strom sendet resync
	signal chan struct{} // es gibt etwas abzuholen
}

func newQueue() *queue {
	return &queue{items: map[key]*item{}, signal: make(chan struct{}, 1)}
}

// get liefert den Eintrag zum Schlüssel und rückt ihn ans Ende: Seine Version ist dann die
// neueste, die Versionen im Strom bleiben aufsteigend (V3). q.mu ist gesperrt.
func (q *queue) get(k key, version uint64) *item {
	it := q.items[k]
	if it == nil {
		it = &item{key: k}
		q.items[k] = it
	}
	q.seq++
	it.version, it.seq = max(it.version, version), q.seq
	return it
}

func (q *queue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// push nimmt ein Ereignis auf. FactsChanged nimmt nur die Elemente, für die watched gilt.
func (q *queue) push(e core.Event, watched func(uuid.UUID) bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.resync {
		return // was jetzt kommt, steht im Stand, den der Browser neu lädt
	}
	v := e.Version()
	switch e := e.(type) {
	case core.ElementsChanged:
		it := q.get(key{kind: kindElements}, v)
		for _, ids := range [][]uuid.UUID{e.Added, e.Changed, e.Missing, e.Returned} {
			for _, id := range ids {
				it.ids = set(it.ids, id)
			}
		}
		for _, id := range e.Deleted {
			it.deleted = set(it.deleted, id)
		}
		it.links = it.links || e.LinksChanged
	case core.HealthChanged:
		it := q.get(key{kind: kindHealth}, v)
		for _, h := range e.Health {
			it.ids = set(it.ids, h.ElementID)
		}
	case core.FactsChanged:
		pushed := false
		for id, keys := range e.Changed {
			if !watched(id) {
				continue
			}
			it := q.get(key{kindFacts, id}, v)
			if it.facts == nil {
				it.facts = map[string]bool{}
			}
			for _, k := range keys {
				it.facts[k] = true
			}
			pushed = true
		}
		if !pushed {
			return
		}
	case core.FindingRaised:
		q.get(key{kindFinding, e.Finding.ID}, v).finding.raised = true
	case core.FindingAcked:
		q.get(key{kindFinding, e.Finding.ID}, v).finding.acked = true
	case core.FindingCauseChanged:
		q.get(key{kindFinding, e.FindingID}, v).finding.cause = true
	case core.FindingEnded:
		q.get(key{kindFinding, e.Finding.ID}, v).finding.ended = &e
	case core.RunChanged:
		q.get(key{kindRun, e.Run.ID}, v).run = e.Run
	case core.SourceStatusChanged:
		q.get(key{kindSource, e.SourceID}, v).source = e.Status
	default:
		return
	}
	q.wake()
}

// requestResync verwirft alles Gesammelte; der Strom sendet resync.
func (q *queue) requestResync() {
	q.mu.Lock()
	q.items, q.resync = map[key]*item{}, true
	q.mu.Unlock()
	q.wake()
}

// take holt alles Gesammelte ab, aufsteigend nach Version.
func (q *queue) take() (resync bool, items []*item) {
	q.mu.Lock()
	defer q.mu.Unlock()
	resync, q.resync = q.resync, false
	for _, it := range q.items {
		items = append(items, it)
	}
	clear(q.items)
	slices.SortFunc(items, func(a, b *item) int { return cmp.Or(cmp.Compare(a.version, b.version), cmp.Compare(a.seq, b.seq)) })
	return resync, items
}

func set(m map[uuid.UUID]bool, id uuid.UUID) map[uuid.UUID]bool {
	if m == nil {
		m = map[uuid.UUID]bool{}
	}
	m[id] = true
	return m
}
