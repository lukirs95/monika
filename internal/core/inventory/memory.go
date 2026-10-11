package inventory

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

// Inventory hält das Inventar im Speicher und schreibt Änderungen über den Store, immer erst die
// Datenbank, dann den Speicher. Es ist Logik ohne eigene Goroutinen; Besitzer ist die Schleife des
// Core, deshalb gibt es keine Locks (docs/contracts/core.md).
type Inventory struct {
	store   Store
	drivers func(name string) (sdk.Driver, bool)
	log     *slog.Logger
	newID   func() uuid.UUID
	now     func() time.Time

	elements map[uuid.UUID]Element
	sources  map[uuid.UUID]Source
	links    map[uuid.UUID]Link
	byExt    map[extKey]uuid.UUID // (Quelle, external_id) → Element; "" = Zugang
	byRef    map[string]uuid.UUID // global_ref → Element

	lastSeenWrite map[uuid.UUID]time.Time // je Quelle: letzter Abschluss einer Discovery in der Datenbank

	graph *Graph // nil = nach einer Änderung neu bauen; jede Änderung im Speicher setzt es zurück
}

type extKey struct {
	source uuid.UUID
	ext    string
}

// New liefert ein leeres Inventar; drivers ist meist sdk.Lookup.
func New(store Store, drivers func(name string) (sdk.Driver, bool), log *slog.Logger) *Inventory {
	return &Inventory{
		store: store, drivers: drivers, log: log,
		newID:         func() uuid.UUID { return uuid.Must(uuid.NewV7()) },
		now:           time.Now,
		elements:      map[uuid.UUID]Element{},
		sources:       map[uuid.UUID]Source{},
		links:         map[uuid.UUID]Link{},
		byExt:         map[extKey]uuid.UUID{},
		byRef:         map[string]uuid.UUID{},
		lastSeenWrite: map[uuid.UUID]time.Time{},
	}
}

// Load übernimmt das gesamte Inventar, wie es beim Start aus der Datenbank kommt.
func (inv *Inventory) Load(snap Snapshot) {
	inv.graph = nil
	for _, s := range snap.Sources {
		inv.sources[s.ID] = s
	}
	for _, e := range snap.Elements {
		inv.putElement(e)
	}
	for _, l := range snap.Links {
		inv.links[l.ID] = l
	}
}

// Restore lädt das gesamte Inventar aus dem Store (beim Start).
func (inv *Inventory) Restore(ctx context.Context) error {
	snap, err := inv.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("load inventory: %w", err)
	}
	inv.Load(snap)
	return nil
}

// Element liefert ein Element.
func (inv *Inventory) Element(id uuid.UUID) (Element, bool) {
	e, ok := inv.elements[id]
	return e, ok
}

// Source liefert eine Quelle.
func (inv *Inventory) Source(id uuid.UUID) (Source, bool) {
	s, ok := inv.sources[id]
	return s, ok
}

// ByExternalID liefert das Element einer Quelle zu seiner external_id; "" ist der Zugang.
func (inv *Inventory) ByExternalID(source uuid.UUID, ext string) (uuid.UUID, bool) {
	id, ok := inv.byExt[extKey{source, ext}]
	return id, ok
}

// Elements, Sources und Links liefern Kopien der Maps für die View; die Werte sind geteilt.
func (inv *Inventory) Elements() map[uuid.UUID]Element { return maps.Clone(inv.elements) }
func (inv *Inventory) Sources() map[uuid.UUID]Source   { return maps.Clone(inv.sources) }
func (inv *Inventory) Links() map[uuid.UUID]Link       { return maps.Clone(inv.links) }

// FactsByID übersetzt die Fakten einer Meldung von external_ids in Element-IDs. Fakten an
// unbekannten Elementen fallen weg; ihre external_ids kommen zurück, damit die Schleife sie
// einmal pro Quelle loggt.
func (inv *Inventory) FactsByID(source uuid.UUID, facts map[string]sdk.Facts) (map[uuid.UUID]sdk.Facts, []string) {
	out := make(map[uuid.UUID]sdk.Facts, len(facts))
	var unknown []string
	for ext, f := range facts {
		if id, ok := inv.byExt[extKey{source, ext}]; ok {
			out[id] = f
		} else {
			unknown = append(unknown, ext)
		}
	}
	slices.Sort(unknown)
	return out, unknown
}

// Path ist der Anzeigename eines Elements: Label, sonst Name, vom Gerät bis zum Element,
// getrennt durch " / ", ohne Zugang (docs/ARCHITECTURE.md, Abschnitt 5). Dieselbe Regel nutzt
// die View reporting.element.
func (inv *Inventory) Path(id uuid.UUID) string { return path(inv.elements, id) }

func path(elements map[uuid.UUID]Element, id uuid.UUID) string {
	var parts []string
	for seen := map[uuid.UUID]bool{}; id != uuid.Nil && !seen[id]; {
		seen[id] = true
		e, ok := elements[id]
		if !ok {
			break
		}
		if e.Kind != KindAccess {
			if name := cmp.Or(e.Label, e.Name); name != "" {
				parts = append(parts, name)
			}
		}
		id = e.ParentID
	}
	slices.Reverse(parts)
	return strings.Join(parts, " / ")
}

func (inv *Inventory) putElement(e Element) {
	inv.graph = nil
	if old, ok := inv.elements[e.ID]; ok && old.GlobalRef != e.GlobalRef && inv.byRef[old.GlobalRef] == e.ID {
		delete(inv.byRef, old.GlobalRef)
	}
	inv.elements[e.ID] = e
	if e.SourceID != uuid.Nil {
		inv.byExt[extKey{e.SourceID, e.ExternalID}] = e.ID
	}
	if e.GlobalRef != "" {
		inv.byRef[e.GlobalRef] = e.ID
	}
}

func (inv *Inventory) deleteElement(id uuid.UUID) {
	e, ok := inv.elements[id]
	if !ok {
		return
	}
	inv.graph = nil
	delete(inv.elements, id)
	if e.SourceID != uuid.Nil {
		delete(inv.byExt, extKey{e.SourceID, e.ExternalID})
	}
	if e.GlobalRef != "" && inv.byRef[e.GlobalRef] == id {
		delete(inv.byRef, e.GlobalRef)
	}
	for lid, l := range inv.links {
		if l.FromID == id || l.ToID == id {
			delete(inv.links, lid)
		}
	}
}
