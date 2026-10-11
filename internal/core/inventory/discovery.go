package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

// Diff beschreibt, was die Discovery bzw. ein Befehl am Inventar geändert hat. Missing und
// Returned sind Wechsel von present.
type Diff struct {
	Added        []uuid.UUID
	Changed      []uuid.UUID
	Missing      []uuid.UUID
	Returned     []uuid.UUID
	Deleted      []uuid.UUID
	LinksChanged bool
}

// ErrGlobalRefConflict bedeutet, dass eine global_ref des Snapshots doppelt vorkommt oder schon
// einem Element einer anderen Quelle gehört.
var ErrGlobalRefConflict = errors.New("global ref conflict")

// seenWriteInterval: Ohne Strukturänderung schreibt ApplySnapshot last_seen_at höchstens so oft.
const seenWriteInterval = 5 * time.Minute

// ApplySnapshot gleicht den vollständigen Stand einer Quelle mit dem Inventar ab: erst die
// Datenbank (ApplyDiscovery), dann der Speicher. Schlägt der Store fehl, bleibt der Speicher
// unverändert. Ungültige Elemente und Links verwirft es mit Warn-Log.
func (inv *Inventory) ApplySnapshot(ctx context.Context, sourceID uuid.UUID, s sdk.Snapshot) (Diff, error) {
	src, ok := inv.sources[sourceID]
	if !ok {
		return Diff{}, fmt.Errorf("source %s: %w", sourceID, ErrNotFound)
	}
	drv, ok := inv.drivers(src.Driver)
	if !ok {
		return Diff{}, fmt.Errorf("source %s: driver %q is not registered", sourceID, src.Driver)
	}
	log := inv.log.With(slog.String("source", sourceID.String()), slog.String("driver", src.Driver))
	now := inv.now()

	globalRef := s.GlobalRef
	if globalRef != "" {
		if err := drv.CheckGlobalRef(globalRef); err != nil {
			log.Warn("global ref of the device ignored", "err", err)
			globalRef = ""
		}
	}
	elements := validElements(drv, s.Elements, log)
	if err := inv.checkRefs(sourceID, globalRef, elements); err != nil {
		return Diff{}, err
	}

	d := Discovery{SourceID: sourceID}
	var diff Diff
	ids := map[string]uuid.UUID{"": src.AccessElementID}
	refs := map[string]uuid.UUID{}
	if globalRef != "" {
		refs[globalRef] = src.AccessElementID
	}
	seen := map[uuid.UUID]bool{src.AccessElementID: true}
	for _, e := range elements {
		id, known := inv.byExt[extKey{sourceID, e.ExternalID}]
		if !known {
			id = inv.newID()
		}
		ids[e.ExternalID] = id
		seen[id] = true
		if e.GlobalRef != "" {
			refs[e.GlobalRef] = id
		}
		old, ok := inv.elements[id]
		if !ok {
			old.Monitored = true // Standardwert der Spalte
		}
		next, err := withDriverFields(old, e)
		if err != nil {
			return Diff{}, fmt.Errorf("source %s: %w", sourceID, err)
		}
		next.ID, next.DeviceID, next.SourceID, next.ParentID = id, src.DeviceID, sourceID, ids[e.ParentID]
		switch {
		case !known:
			diff.Added = append(diff.Added, id)
			d.Upsert = append(d.Upsert, next)
		case !sameDriverFields(old, next):
			diff.Changed = append(diff.Changed, id)
			d.Upsert = append(d.Upsert, next)
		}
		if known && !old.Present {
			diff.Returned = append(diff.Returned, id)
		}
	}
	vanishes := false
	for id := range inv.elementsBySource[sourceID] {
		if e := inv.elements[id]; e.Present && !seen[id] {
			vanishes = true
			break
		}
	}
	if id := Identity(s.Identity); id != src.Identity {
		d.Identity = &id
	}
	if inv.elements[src.AccessElementID].GlobalRef != globalRef {
		d.GlobalRef = &globalRef
	}
	d.AddLinks, d.RemoveLinks = inv.diffLinks(sourceID, s.Links, ids, refs, log)

	structural := len(d.Upsert) > 0 || len(d.AddLinks) > 0 || len(d.RemoveLinks) > 0 ||
		d.Identity != nil || d.GlobalRef != nil || len(diff.Returned) > 0 || vanishes
	if !structural && now.Sub(inv.lastSeenWrite[sourceID]) < seenWriteInterval {
		return Diff{}, nil
	}
	d.Seen = slices.Collect(maps.Keys(seen))
	missing, err := inv.store.ApplyDiscovery(ctx, d)
	if err != nil {
		return Diff{}, fmt.Errorf("apply discovery of source %s: %w", sourceID, err)
	}
	inv.lastSeenWrite[sourceID] = now
	if structural {
		// Nur dann; sonst hat sich nur last_seen_at geändert, und der Graph bleibt (mit dem
		// vorigen LastSeenAt) bestehen, statt alle 5 min pro Quelle neu gebaut zu werden.
		inv.graph = nil
	}

	// Ab hier nur noch der Speicher, in derselben Reihenfolge wie der Store.
	if d.Identity != nil {
		src.Identity = *d.Identity
		inv.sources[sourceID] = src
	}
	for _, e := range d.Upsert {
		inv.putElement(e)
	}
	for id := range seen {
		e := inv.elements[id]
		e.Present, e.LastSeenAt = true, now
		if e.FirstSeenAt.IsZero() {
			e.FirstSeenAt = now
		}
		inv.elements[id] = e
	}
	for _, id := range missing {
		e := inv.elements[id]
		e.Present = false
		inv.elements[id] = e
	}
	diff.Missing = missing
	resolved := false
	if d.GlobalRef != nil {
		access := inv.elements[src.AccessElementID]
		access.GlobalRef = globalRef
		inv.putElement(access)
		if globalRef != "" {
			resolved = inv.resolveRefs(globalRef, access.ID) || resolved
		}
	}
	for _, id := range d.RemoveLinks {
		inv.delLink(id)
	}
	for _, l := range d.AddLinks {
		inv.setLink(l)
	}
	for _, e := range d.Upsert {
		if e.GlobalRef != "" {
			resolved = inv.resolveRefs(e.GlobalRef, e.ID) || resolved
		}
	}
	diff.LinksChanged = len(d.AddLinks) > 0 || len(d.RemoveLinks) > 0 || resolved
	return diff, nil
}

// validElements verwirft ungültige Elemente mit Warn-Log und liefert die übrigen mit den Eltern
// vor den Kindern, wie der Store sie schreiben muss. Ein Element ohne gültigen Weg zum Zugang
// (unbekannte oder verworfene Eltern, Zyklus) fällt mit seinen Kindern weg.
func validElements(drv sdk.Driver, elements []sdk.Element, log *slog.Logger) []sdk.Element {
	byExt := make(map[string]sdk.Element, len(elements))
	for _, e := range elements {
		if err := drv.CheckElement(e); err != nil {
			log.Warn("element dropped", "err", err)
			continue
		}
		if _, dup := byExt[e.ExternalID]; dup {
			log.Warn("element dropped", "external_id", e.ExternalID, "err", "duplicate external ID")
			continue
		}
		byExt[e.ExternalID] = e
	}

	const (
		open = iota
		visiting
		accepted
		dropped
	)
	state := make(map[string]int, len(byExt))
	out := make([]sdk.Element, 0, len(byExt))
	var visit func(ext string) bool
	visit = func(ext string) bool {
		switch state[ext] {
		case accepted:
			return true
		case visiting, dropped:
			return false
		}
		e, ok := byExt[ext]
		if !ok {
			return false
		}
		state[ext] = visiting
		if e.ParentID != "" && !visit(e.ParentID) {
			state[ext] = dropped
			log.Warn("element dropped", "external_id", ext, "err", fmt.Sprintf("parent %q is unknown, invalid or part of a cycle", e.ParentID))
			return false
		}
		state[ext] = accepted
		out = append(out, e)
		return true
	}
	for _, e := range elements {
		if _, ok := byExt[e.ExternalID]; ok {
			visit(e.ExternalID)
		}
	}
	return out
}

// checkRefs prüft, dass jede global_ref des Snapshots höchstens einmal vorkommt und keinem
// Element einer anderen Quelle gehört; sonst schlüge der Unique-Index in der Datenbank zu.
func (inv *Inventory) checkRefs(sourceID uuid.UUID, globalRef string, elements []sdk.Element) error {
	seen := map[string]bool{}
	check := func(ref string) error {
		if seen[ref] {
			return fmt.Errorf("%w: %q is reported twice", ErrGlobalRefConflict, ref)
		}
		seen[ref] = true
		if id, ok := inv.byRef[ref]; ok && inv.elements[id].SourceID != sourceID {
			return fmt.Errorf("%w: %q belongs to element %s of source %s", ErrGlobalRefConflict, ref, id, inv.elements[id].SourceID)
		}
		return nil
	}
	var errs []error
	if globalRef != "" {
		errs = append(errs, check(globalRef))
	}
	for _, e := range elements {
		if e.GlobalRef != "" {
			errs = append(errs, check(e.GlobalRef))
		}
	}
	return errors.Join(errs...)
}

type linkKey struct {
	typ            LinkType
	fromID, toID   uuid.UUID
	fromRef, toRef string
}

func keyOf(l Link) linkKey {
	return linkKey{l.Type, l.FromID, l.ToID, l.FromRef, l.ToRef}
}

// diffLinks vergleicht die gemeldeten Links mit den bestehenden Treiber-Links der Quelle.
// Endpunkte per global_ref werden aufgelöst, wenn das Ziel bekannt ist (refs: aus diesem
// Snapshot, sonst das Inventar), und bleiben sonst als Verweis stehen.
func (inv *Inventory) diffLinks(sourceID uuid.UUID, links []sdk.Link, ids, refs map[string]uuid.UUID, log *slog.Logger) (add []Link, remove []uuid.UUID) {
	existing := map[linkKey]uuid.UUID{}
	for id := range inv.linksBySource[sourceID] {
		if l := inv.links[id]; l.Type != LinkMember {
			existing[keyOf(l)] = id
		}
	}
	endpoint := func(ep sdk.Endpoint) (uuid.UUID, string, bool) {
		if ep.GlobalRef != "" {
			if id, ok := refs[ep.GlobalRef]; ok {
				return id, "", true
			}
			if id, ok := inv.byRef[ep.GlobalRef]; ok {
				return id, "", true
			}
			return uuid.Nil, ep.GlobalRef, true
		}
		id, ok := ids[ep.ExternalID]
		return id, "", ok
	}
	want := map[linkKey]bool{}
	for _, sl := range links {
		l := Link{Type: LinkType(sl.Type), SourceID: sourceID}
		var fromOK, toOK bool
		l.FromID, l.FromRef, fromOK = endpoint(sl.From)
		l.ToID, l.ToRef, toOK = endpoint(sl.To)
		var problem string
		switch {
		case sl.Type != sdk.LinkFeeds && sl.Type != sdk.LinkUses && sl.Type != sdk.LinkRepresents:
			problem = fmt.Sprintf("unknown type %q", sl.Type)
		case sl.Type == sdk.LinkRepresents && sl.To.GlobalRef == "":
			problem = "represents must point to its original by global ref"
		case !fromOK || !toOK:
			problem = "unknown or dropped external ID"
		}
		if problem != "" {
			log.Warn("link dropped", "type", sl.Type, "from", sl.From, "to", sl.To, "err", problem)
			continue
		}
		key := keyOf(l)
		if want[key] {
			continue
		}
		want[key] = true
		if _, ok := existing[key]; ok {
			delete(existing, key)
			continue
		}
		l.ID = inv.newID()
		add = append(add, l)
	}
	remove = slices.Collect(maps.Values(existing))
	return add, remove
}

// resolveRefs löst unaufgelöste Endpunkte mit dieser global_ref auf, in allen Quellen.
func (inv *Inventory) resolveRefs(ref string, id uuid.UUID) bool {
	lids := slices.Collect(maps.Keys(inv.linksByRef[ref]))
	for _, lid := range lids {
		l := inv.links[lid]
		if l.FromRef == ref {
			l.FromID, l.FromRef = id, ""
		}
		if l.ToRef == ref {
			l.ToID, l.ToRef = id, ""
		}
		inv.setLink(l)
	}
	return len(lids) > 0
}

// withDriverFields setzt die Spalten, die der Treiber liefert, und lässt die übrigen stehen.
func withDriverFields(e Element, se sdk.Element) (Element, error) {
	e.Kind = Kind(se.Kind)
	e.ExternalID = se.ExternalID
	e.GlobalRef = se.GlobalRef
	e.Transport = se.Transport
	e.Direction = Direction(se.Direction)
	e.Name = se.Name
	e.Attributes = se.Attributes
	e.Position = nil
	if se.Position != nil {
		e.Position = new(*se.Position)
	}
	e.Actions = slices.Clone(se.Actions)
	e.CriticalActions = slices.Clone(se.CriticalActions)
	var err error
	if e.Settings, err = marshalNonEmpty(se.Settings); err != nil {
		return Element{}, fmt.Errorf("element %q: settings: %w", se.ExternalID, err)
	}
	if e.Params, err = marshalNonEmpty(se.Params); err != nil {
		return Element{}, fmt.Errorf("element %q: params: %w", se.ExternalID, err)
	}
	return e, nil
}

func marshalNonEmpty[M ~map[K]V, K comparable, V any](m M) (json.RawMessage, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

// sameDriverFields vergleicht die Spalten, die der Treiber liefert.
func sameDriverFields(a, b Element) bool {
	return a.Kind == b.Kind && a.ParentID == b.ParentID && a.GlobalRef == b.GlobalRef &&
		a.Transport == b.Transport && a.Direction == b.Direction && a.Name == b.Name &&
		(a.Position == nil) == (b.Position == nil) && (a.Position == nil || *a.Position == *b.Position) &&
		slices.Equal(a.Actions, b.Actions) && slices.Equal(a.CriticalActions, b.CriticalActions) &&
		jsonEqual(a.Attributes, b.Attributes) && jsonEqual(a.Settings, b.Settings) && jsonEqual(a.Params, b.Params)
}

// jsonEqual vergleicht JSON nach Inhalt, denn PostgreSQL liefert jsonb mit anderer Reihenfolge
// der Schlüssel und anderen Leerzeichen zurück. Leer, null und {} sind gleich.
func jsonEqual(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	if emptyJSON(a) || emptyJSON(b) {
		return emptyJSON(a) && emptyJSON(b)
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func emptyJSON(m json.RawMessage) bool {
	s := string(bytes.TrimSpace(m))
	return s == "" || s == "null" || s == "{}"
}
