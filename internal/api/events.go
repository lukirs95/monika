package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
)

// Ereignisstrom (SSE) und Abos (api.md, Stand, Ereignisse und Abos).

const (
	writeDeadline = 30 * time.Second // hängt ein Schreiben länger, ist die Verbindung tot
	watchLimit    = 1000
)

// streamTimes sind die Takte eines Stroms; Tests verkürzen sie.
type streamTimes struct {
	keepalive time.Duration // so lange still, dann ein Kommentar
	recheck   time.Duration // Anmeldung erneut prüfen, ohne Aktivität
}

var defaultStreamTimes = streamTimes{keepalive: 30 * time.Second, recheck: 60 * time.Second}

func (s *Server) GetEvents(ctx context.Context, rq oapi.GetEventsRequestObject) (oapi.GetEventsResponseObject, error) {
	if _, err := s.view(); err != nil {
		return nil, err
	}
	rqi := requestOf(ctx)
	st, hello, err := s.dist.open(rqi.principal, value(rq.Params.LastEventID))
	if errors.Is(err, errNoStream) {
		return nil, core.ErrStopped
	} else if err != nil {
		return nil, err
	}
	return &eventStream{s: s, ctx: ctx, rq: rqi, st: st, hello: hello}, nil
}

// eventStream schreibt den Strom; der strict server ruft VisitGetEventsResponse und wartet, bis
// der Strom endet.
type eventStream struct {
	s     *Server
	ctx   context.Context
	rq    request
	st    *stream
	hello uint64

	w     http.ResponseWriter
	rc    *http.ResponseController
	view  *core.View
	sight sight
	known known
}

// known ist, was der Browser über die Struktur weiß: sichtbare Elemente, Gerüst und Links. Mit
// ihm vergleicht der Strom bei elements_changed (V4).
type known struct {
	visible, skeleton map[uuid.UUID]bool
	links             map[uuid.UUID]inventory.Link
}

func (es *eventStream) VisitGetEventsResponse(w http.ResponseWriter) error {
	defer es.s.dist.close(es.st)
	es.w, es.rc = w, http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // Proxys puffern den Strom nicht
	w.WriteHeader(http.StatusOK)

	es.reset(es.s.cfg.Core.View())
	// hello ohne SSE-id: Seine Version heißt „alles danach kommt“, eine Last-Event-ID dagegen
	// „ab einschließlich“. Bricht der Strom vor dem ersten Ereignis ab, beginnt der Browser neu.
	if err := es.send("hello", 0, oapi.EventHello{StreamID: es.st.id, Version: int64(es.hello)}); err != nil {
		return nil
	}
	if err := es.flush(); err != nil {
		return nil
	}
	ping := time.NewTicker(es.s.times.keepalive)
	defer ping.Stop()
	recheck := time.NewTicker(es.s.times.recheck)
	defer recheck.Stop()
	for {
		var err error
		select {
		case <-es.ctx.Done():
			return nil
		case <-es.st.closed:
			return nil
		case <-recheck.C:
			if !es.stillAuthenticated() {
				return nil
			}
			continue
		case <-ping.C:
			err = es.write(": ping\n\n")
		case <-es.st.signal:
			err = es.deliver()
		}
		if err == nil {
			err = es.flush()
		}
		if err != nil {
			return nil // Verbindung tot oder zu langsam; der Browser verbindet neu
		}
		ping.Reset(es.s.times.keepalive)
	}
}

// stillAuthenticated prüft Sitzung bzw. Token erneut, ohne Aktivität (E5). Nur eine ungültige
// Anmeldung beendet den Strom; ist die Datenbank kurz weg, läuft er weiter.
func (es *eventStream) stillAuthenticated() bool {
	var err error
	if es.rq.session != "" {
		err = es.s.cfg.Auth.CheckSession(es.ctx, es.rq.session)
	} else {
		err = es.s.cfg.Auth.CheckToken(es.ctx, es.rq.bearer)
	}
	return !errors.Is(err, auth.ErrUnauthenticated)
}

// reset setzt Sicht und Bekanntes auf eine View, beim Öffnen und bei resync.
func (es *eventStream) reset(v *core.View) {
	es.view, es.sight = v, es.s.sight(v, es.st.principal)
	es.known = es.sight.known()
	es.resolveWatched()
}

// deliver holt das Gesammelte ab und schreibt es, aufsteigend nach Version.
func (es *eventStream) deliver() error {
	resync, items := es.st.take()
	v := es.s.cfg.Core.View()
	if resync {
		es.reset(v)
		return es.write("event: resync\ndata: {}\n\n")
	}
	if v.Graph != es.view.Graph {
		es.sight = es.s.sight(v, es.st.principal)
	}
	es.view = v
	for _, it := range items {
		if err := es.item(it); err != nil {
			return err
		}
	}
	return nil
}

func (es *eventStream) item(it *item) error {
	v, si := es.view, es.sight
	switch it.key.kind {
	case kindElements:
		ev := es.elementsChanged(it)
		es.resolveWatched()
		if len(ev.Elements)+len(ev.Skeleton)+len(ev.RemovedElements)+len(ev.Links)+len(ev.RemovedLinks) == 0 {
			return nil
		}
		return es.send("elements_changed", it.version, ev)
	case kindHealth:
		ev := oapi.EventHealthChanged{Health: []oapi.ElementHealth{}}
		for _, id := range sortedIDs(it.ids) {
			if !si.sees(id) {
				continue
			}
			h, ok := v.Health[id]
			if !ok {
				h = evaluation.ElementHealth{ElementID: id}
			}
			ev.Health = append(ev.Health, toHealth(h))
		}
		if len(ev.Health) == 0 {
			return nil
		}
		return es.send("health_changed", it.version, ev)
	case kindFacts:
		id := it.key.id
		if !si.sees(id) || !es.st.watching(id) {
			return nil
		}
		ev := oapi.EventFactsChanged{ElementID: id, Facts: oapi.Facts{}}
		var removed []string
		for _, k := range slices.Sorted(maps.Keys(it.facts)) {
			if val, ok := v.Facts[id][k]; ok {
				ev.Facts[k] = val
			} else {
				removed = append(removed, k)
			}
		}
		if removed != nil {
			ev.Removed = &removed
		}
		return es.send("facts_changed", it.version, ev)
	case kindFinding:
		return es.finding(it)
	case kindRun:
		if !si.seesRun(it.run.ElementID) {
			return nil
		}
		return es.send("run_changed", it.version, toRunSummary(it.run))
	case kindSource:
		src, ok := v.Graph.Source(it.key.id)
		if !ok || !si.sees(src.AccessElementID) {
			return nil
		}
		return es.send("source_status_changed", it.version, toSourceStatus(it.key.id, it.source))
	}
	return nil
}

// finding schreibt den Stand eines Befunds: beendet als finding_ended, aktiv mit dem Befund aus
// der View (V2).
func (es *eventStream) finding(it *item) error {
	f := it.finding
	if e := f.ended; e != nil {
		if !es.sight.sees(e.Finding.ElementID) && !es.known.visible[e.Finding.ElementID] {
			return nil
		}
		return es.send("finding_ended", it.version, oapi.EventFindingEnded{
			FindingID: e.Finding.ID, ClearedAt: e.Finding.ClearedAt.UTC(),
			Reason: oapi.EventFindingEndedReason(e.Reason), Withdrawn: e.Reason != evaluation.ReasonResolved,
		})
	}
	active, ok := es.view.Findings[it.key.id]
	if !ok || !es.sight.sees(active.ElementID) {
		return nil // inzwischen beendet: finding_ended folgt
	}
	switch {
	case f.raised:
		return es.send("finding_raised", it.version, toFinding(active))
	case f.acked:
		return es.send("finding_acked", it.version, toFinding(active))
	}
	return es.send("finding_cause_changed", it.version, oapi.EventFindingCauseChanged{
		FindingID: active.ID, CausedByID: optionalID(active.CausedByID),
	})
}

// elementsChanged vergleicht die neue Sicht mit dem, was der Browser kennt (V4). Ein Element
// steht in elements oder skeleton; der Browser übernimmt es in die jeweilige Liste und nimmt es
// aus der anderen. removed_elements kennt der Browser danach gar nicht mehr.
func (es *eventStream) elementsChanged(it *item) oapi.EventElementsChanged {
	g, now := es.view.Graph, es.sight.known()
	ev := oapi.EventElementsChanged{
		Elements: []oapi.Element{}, Skeleton: []oapi.SkeletonElement{}, RemovedElements: []uuid.UUID{},
		Links: []oapi.Link{}, RemovedLinks: []uuid.UUID{},
	}
	for id := range now.visible {
		if it.ids[id] || !es.known.visible[id] {
			e, _ := g.Element(id)
			ev.Elements = append(ev.Elements, toElement(e))
		}
	}
	for id := range now.skeleton {
		if it.ids[id] || !es.known.skeleton[id] {
			e, _ := g.Element(id)
			ev.Skeleton = append(ev.Skeleton, toSkeleton(e))
		}
	}
	for _, old := range []map[uuid.UUID]bool{es.known.visible, es.known.skeleton} {
		for id := range old {
			if !now.visible[id] && !now.skeleton[id] && !slices.Contains(ev.RemovedElements, id) {
				ev.RemovedElements = append(ev.RemovedElements, id)
			}
		}
	}
	for id, l := range now.links {
		if old, ok := es.known.links[id]; !ok || !sameLink(old, l) {
			ev.Links = append(ev.Links, toLink(l))
		}
	}
	for id := range es.known.links {
		if _, ok := now.links[id]; !ok {
			ev.RemovedLinks = append(ev.RemovedLinks, id)
		}
	}
	slices.SortFunc(ev.Elements, func(a, b oapi.Element) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(ev.Skeleton, func(a, b oapi.SkeletonElement) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(ev.RemovedElements, compareIDs)
	slices.SortFunc(ev.Links, func(a, b oapi.Link) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(ev.RemovedLinks, compareIDs)
	es.known = now
	return ev
}

// sameLink vergleicht zwei Stände eines Links; Position nach Wert.
func sameLink(a, b inventory.Link) bool {
	pa, pb := a.Position, b.Position
	a.Position, b.Position = nil, nil
	return a == b && (pa == nil) == (pb == nil) && (pa == nil || *pa == *pb)
}

// resolveWatched löst die beobachteten Geltungsbereiche gegen die aktuelle Sicht auf (V5).
func (es *eventStream) resolveWatched() {
	es.st.mu.Lock()
	defer es.st.mu.Unlock()
	es.st.watched = resolveScopes(es.view, es.sight, es.st.scopes)
}

func resolveScopes(v *core.View, si sight, scopes map[uuid.UUID]bool) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	for scope := range scopes {
		for id := range v.Graph.ScopeElements(scope) {
			if si.sees(id) {
				out[id] = true
			}
		}
	}
	return out
}

// send schreibt ein Ereignis; id ist die Version, zu der es den Stand beschreibt (0 = ohne id).
func (es *eventStream) send(event string, version uint64, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode %s: %w", event, err)
	}
	id := ""
	if version > 0 {
		id = "id: " + strconv.FormatUint(version, 10) + "\n"
	}
	return es.write(id + "event: " + event + "\ndata: " + string(b) + "\n\n")
}

func (es *eventStream) write(s string) error {
	if err := es.rc.SetWriteDeadline(time.Now().Add(writeDeadline)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	_, err := es.w.Write([]byte(s))
	return err
}

func (es *eventStream) flush() error { return es.rc.Flush() }

// known liefert, was die Sicht zeigt: sichtbare Elemente, Gerüst und sichtbare Links.
func (si sight) known() known {
	k := known{visible: map[uuid.UUID]bool{}, skeleton: map[uuid.UUID]bool{}, links: map[uuid.UUID]inventory.Link{}}
	for id := range si.v.Graph.All() {
		if si.sees(id) {
			k.visible[id] = true
		}
	}
	for _, id := range si.skeleton() {
		k.skeleton[id] = true
	}
	for id, l := range si.v.Graph.Links() {
		if si.seesLink(l) {
			k.links[id] = l
		}
	}
	return k
}

func sortedIDs(m map[uuid.UUID]bool) []uuid.UUID {
	return slices.SortedFunc(maps.Keys(m), compareIDs)
}

func (s *Server) Watch(ctx context.Context, rq oapi.WatchRequestObject) (oapi.WatchResponseObject, error) {
	p := principal(ctx)
	st, ok := s.dist.stream(rq.StreamID)
	if !ok || st.principal.UserID != p.UserID {
		return nil, errNotFound // V6
	}
	v, err := s.view()
	if err != nil {
		return nil, err
	}
	si := s.sight(v, p)
	add, remove := value(rq.Body.Add), value(rq.Body.Remove)
	for _, id := range add {
		if !si.sees(id) {
			return nil, errNotFound
		}
	}
	st.mu.Lock()
	scopes := maps.Clone(st.scopes)
	for _, id := range remove {
		delete(scopes, id)
	}
	for _, id := range add {
		scopes[id] = true
	}
	watched := resolveScopes(v, si, scopes)
	if len(watched) > watchLimit {
		st.mu.Unlock()
		return nil, problem(http.StatusUnprocessableEntity, "watch_limit", fmt.Sprintf("at most %d watched elements, got %d", watchLimit, len(watched)))
	}
	before := st.watched
	st.scopes, st.watched = scopes, watched
	st.mu.Unlock()

	// Erst beobachten, dann die View lesen: Was danach geschieht, kommt als Ereignis; was davor
	// lag, steht in der Antwort.
	v, err = s.view()
	if err != nil {
		return nil, err
	}
	out := oapi.Watch200JSONResponse{Version: int64(v.Version), Watched: len(watched), Facts: []oapi.ElementFacts{}}
	for _, id := range sortedIDs(watched) {
		if !before[id] {
			out.Facts = append(out.Facts, elementFacts(v, id))
		}
	}
	return out, nil
}
