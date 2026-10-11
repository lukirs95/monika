package inventory

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Fehler der Befehle, zusätzlich zu ErrNotFound und ErrMemberCycle aus dem Store.
var (
	ErrUnknownDriver = errors.New("driver not registered")
	ErrNotAGroup     = errors.New("element is not a group")
	ErrMissingID     = errors.New("missing ID")
)

// Die Befehle der API. Jeder schreibt erst über den Store (eine Transaktion mit Audit) und
// ändert danach den Speicher; schlägt der Store fehl, bleibt der Speicher unverändert. Neue IDs
// vergibt der Aufrufer (die API), nur die ID des Zugangs-Elements einer Quelle vergibt das
// Inventar.

// CreateDevice legt ein Gerät an; verwendet ID, Label und Notes.
func (inv *Inventory) CreateDevice(ctx context.Context, actor uuid.UUID, e Element) (Diff, error) {
	if e.ID == uuid.Nil {
		return Diff{}, fmt.Errorf("create device: %w", ErrMissingID)
	}
	dev := Element{ID: e.ID, Kind: KindDevice, DeviceID: e.ID, Label: e.Label, Notes: e.Notes, Present: true, Monitored: true}
	if err := inv.store.CreateDevice(ctx, actor, dev); err != nil {
		return Diff{}, fmt.Errorf("create device: %w", err)
	}
	inv.putElement(dev)
	return Diff{Added: []uuid.UUID{dev.ID}}, nil
}

// DeleteDevice löscht ein Gerät hart, mit seinen Quellen, Elementen und Links.
func (inv *Inventory) DeleteDevice(ctx context.Context, actor uuid.UUID, id uuid.UUID) (Diff, error) {
	if inv.elements[id].Kind != KindDevice {
		return Diff{}, fmt.Errorf("delete device %s: %w", id, ErrNotFound)
	}
	if err := inv.store.DeleteDevice(ctx, actor, id); err != nil {
		return Diff{}, fmt.Errorf("delete device %s: %w", id, err)
	}
	for sid, s := range inv.sources {
		if s.DeviceID == id {
			delete(inv.sources, sid)
			delete(inv.lastSeenWrite, sid)
		}
	}
	return inv.deleteElements(func(e Element) bool { return e.ID == id || e.DeviceID == id }), nil
}

// CreateSource legt eine Quelle samt Zugangs-Element an; verwendet ID, DeviceID, Driver,
// Address, Config, SecretID und Enabled.
func (inv *Inventory) CreateSource(ctx context.Context, actor uuid.UUID, s Source) (Diff, error) {
	if s.ID == uuid.Nil {
		return Diff{}, fmt.Errorf("create source: %w", ErrMissingID)
	}
	if inv.elements[s.DeviceID].Kind != KindDevice {
		return Diff{}, fmt.Errorf("create source: device %s: %w", s.DeviceID, ErrNotFound)
	}
	if _, ok := inv.drivers(s.Driver); !ok {
		return Diff{}, fmt.Errorf("create source: %q: %w", s.Driver, ErrUnknownDriver)
	}
	s.Identity, s.AccessElementID = Identity{}, inv.newID()
	if err := inv.store.CreateSource(ctx, actor, s, s.AccessElementID); err != nil {
		return Diff{}, fmt.Errorf("create source: %w", err)
	}
	inv.sources[s.ID] = s
	inv.putElement(Element{
		ID: s.AccessElementID, Kind: KindAccess, ParentID: s.DeviceID, DeviceID: s.DeviceID, SourceID: s.ID,
		Name: s.Driver, Present: true, Monitored: true,
	})
	return Diff{Added: []uuid.UUID{s.AccessElementID}}, nil
}

// UpdateSource ändert Adresse, Konfiguration, Secret und Aktivierung einer Quelle; Gerät und
// Treiber stehen fest.
func (inv *Inventory) UpdateSource(ctx context.Context, actor uuid.UUID, s Source) (Diff, error) {
	next, ok := inv.sources[s.ID]
	if !ok {
		return Diff{}, fmt.Errorf("update source %s: %w", s.ID, ErrNotFound)
	}
	if _, ok := inv.drivers(next.Driver); !ok {
		return Diff{}, fmt.Errorf("update source %s: %q: %w", s.ID, next.Driver, ErrUnknownDriver)
	}
	next.Address, next.Config, next.SecretID, next.Enabled = s.Address, s.Config, s.SecretID, s.Enabled
	if err := inv.store.UpdateSource(ctx, actor, next); err != nil {
		return Diff{}, fmt.Errorf("update source %s: %w", s.ID, err)
	}
	inv.sources[s.ID] = next
	return Diff{}, nil
}

// DeleteSource löscht eine Quelle hart, mit Zugang, Elementen und Links.
func (inv *Inventory) DeleteSource(ctx context.Context, actor uuid.UUID, id uuid.UUID) (Diff, error) {
	if _, ok := inv.sources[id]; !ok {
		return Diff{}, fmt.Errorf("delete source %s: %w", id, ErrNotFound)
	}
	if err := inv.store.DeleteSource(ctx, actor, id); err != nil {
		return Diff{}, fmt.Errorf("delete source %s: %w", id, err)
	}
	delete(inv.sources, id)
	delete(inv.lastSeenWrite, id)
	return inv.deleteElements(func(e Element) bool { return e.SourceID == id }), nil
}

// CreateGroup legt eine Gruppe an; verwendet ID, Label und Notes.
func (inv *Inventory) CreateGroup(ctx context.Context, actor uuid.UUID, e Element) (Diff, error) {
	if e.ID == uuid.Nil {
		return Diff{}, fmt.Errorf("create group: %w", ErrMissingID)
	}
	g := Element{ID: e.ID, Kind: KindGroup, Label: e.Label, Notes: e.Notes, Present: true, Monitored: true}
	if err := inv.store.CreateGroup(ctx, actor, g); err != nil {
		return Diff{}, fmt.Errorf("create group: %w", err)
	}
	inv.putElement(g)
	return Diff{Added: []uuid.UUID{g.ID}}, nil
}

// DeleteGroup löscht eine Gruppe hart, mit ihren Mitgliedschaften.
func (inv *Inventory) DeleteGroup(ctx context.Context, actor uuid.UUID, id uuid.UUID) (Diff, error) {
	if inv.elements[id].Kind != KindGroup {
		return Diff{}, fmt.Errorf("delete group %s: %w", id, ErrNotFound)
	}
	if err := inv.store.DeleteGroup(ctx, actor, id); err != nil {
		return Diff{}, fmt.Errorf("delete group %s: %w", id, err)
	}
	return inv.deleteElements(func(e Element) bool { return e.ID == id }), nil
}

// UpdateUserFields ändert Label, Notes und Monitored eines Elements.
func (inv *Inventory) UpdateUserFields(ctx context.Context, actor uuid.UUID, id uuid.UUID, f UserFields) (Diff, error) {
	e, ok := inv.elements[id]
	if !ok {
		return Diff{}, fmt.Errorf("update element %s: %w", id, ErrNotFound)
	}
	if err := inv.store.UpdateUserFields(ctx, actor, id, f); err != nil {
		return Diff{}, fmt.Errorf("update element %s: %w", id, err)
	}
	e.Label, e.Notes, e.Monitored = f.Label, f.Notes, f.Monitored
	inv.elements[id] = e
	return Diff{Changed: []uuid.UUID{id}}, nil
}

// AddGroupMember nimmt ein Element in eine Gruppe auf; verwendet ID, FromID (die Gruppe), ToID,
// Position und Label.
func (inv *Inventory) AddGroupMember(ctx context.Context, actor uuid.UUID, l Link) (Diff, error) {
	if l.ID == uuid.Nil {
		return Diff{}, fmt.Errorf("add group member: %w", ErrMissingID)
	}
	group, ok := inv.elements[l.FromID]
	switch {
	case !ok:
		return Diff{}, fmt.Errorf("add group member: group %s: %w", l.FromID, ErrNotFound)
	case group.Kind != KindGroup:
		return Diff{}, fmt.Errorf("add group member: %s: %w", l.FromID, ErrNotAGroup)
	}
	if _, ok := inv.elements[l.ToID]; !ok {
		return Diff{}, fmt.Errorf("add group member: member %s: %w", l.ToID, ErrNotFound)
	}
	m := Link{ID: l.ID, Type: LinkMember, FromID: l.FromID, ToID: l.ToID, Position: l.Position, Label: l.Label}
	if err := inv.store.AddGroupMember(ctx, actor, m); err != nil {
		return Diff{}, fmt.Errorf("add group member: %w", err)
	}
	inv.links[m.ID] = m
	return Diff{LinksChanged: true}, nil
}

// UpdateGroupMember ändert Position und Label einer Mitgliedschaft.
func (inv *Inventory) UpdateGroupMember(ctx context.Context, actor uuid.UUID, l Link) (Diff, error) {
	next, ok := inv.links[l.ID]
	if !ok || next.Type != LinkMember {
		return Diff{}, fmt.Errorf("update group member %s: %w", l.ID, ErrNotFound)
	}
	next.Position, next.Label = l.Position, l.Label
	if err := inv.store.UpdateGroupMember(ctx, actor, next); err != nil {
		return Diff{}, fmt.Errorf("update group member %s: %w", l.ID, err)
	}
	inv.links[l.ID] = next
	return Diff{LinksChanged: true}, nil
}

// RemoveGroupMember beendet eine Mitgliedschaft.
func (inv *Inventory) RemoveGroupMember(ctx context.Context, actor uuid.UUID, id uuid.UUID) (Diff, error) {
	if l, ok := inv.links[id]; !ok || l.Type != LinkMember {
		return Diff{}, fmt.Errorf("remove group member %s: %w", id, ErrNotFound)
	}
	if err := inv.store.RemoveGroupMember(ctx, actor, id); err != nil {
		return Diff{}, fmt.Errorf("remove group member %s: %w", id, err)
	}
	delete(inv.links, id)
	return Diff{LinksChanged: true}, nil
}

// deleteElements entfernt die passenden Elemente mit ihren Links aus dem Speicher, wie die
// Datenbank per CASCADE.
func (inv *Inventory) deleteElements(match func(Element) bool) Diff {
	var diff Diff
	links := len(inv.links)
	for id, e := range inv.elements {
		if match(e) {
			diff.Deleted = append(diff.Deleted, id)
		}
	}
	for _, id := range diff.Deleted {
		inv.deleteElement(id)
	}
	diff.LinksChanged = len(inv.links) != links
	return diff
}
