package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/inventory"
)

// Befehle an das Inventar über Core.Do (core.md, Befehle). Jeder Handler prüft erst die
// Sichtbarkeit (404), dann das Recht (403), schickt den Befehl und liest das Ergebnis aus der
// neuen View.

var errForbidden = problem(http.StatusForbidden, "forbidden", "missing permission")

// newID vergibt die ID eines neuen Objekts (core.md: IDs vergibt die API).
func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// visible meldet, ob das Element existiert und der Benutzer es sehen darf (nicht als Gerüst).
func (s *Server) visible(v *core.View, p auth.Principal, id uuid.UUID) bool {
	_, ok := v.Graph.Element(id)
	return ok && s.cfg.Auth.Allowed(v, p, auth.PermView, id)
}

// visibleKind wie visible, und das Element hat die Art kind.
func (s *Server) visibleKind(v *core.View, p auth.Principal, id uuid.UUID, kind inventory.Kind) bool {
	e, _ := v.Graph.Element(id)
	return e.Kind == kind && s.visible(v, p, id)
}

// require prüft ein Recht am Element; uuid.Nil fragt das globale Recht ab.
func (s *Server) require(v *core.View, p auth.Principal, perm auth.Permission, element uuid.UUID) error {
	if !s.cfg.Auth.Allowed(v, p, perm, element) {
		return errForbidden
	}
	return nil
}

// seesPresets meldet, ob der Benutzer Presets sehen darf (für *_in_use).
func (s *Server) seesPresets(v *core.View, p auth.Principal) bool {
	return s.cfg.Auth.Allowed(v, p, auth.PermPresetRun, uuid.Nil) || s.cfg.Auth.Allowed(v, p, auth.PermPresetEdit, uuid.Nil)
}

// do schickt einen Befehl und liefert die View danach.
func (s *Server) do(ctx context.Context, cmd core.Command) (*core.View, error) {
	if err := s.cfg.Core.Do(ctx, cmd); err != nil {
		return nil, err
	}
	return s.view()
}

// element liest ein Element aus der View nach einem Befehl.
func element(v *core.View, id uuid.UUID) (oapi.Element, error) {
	e, ok := v.Graph.Element(id)
	if !ok {
		return oapi.Element{}, errNotFound // inzwischen wieder gelöscht
	}
	return toElement(e), nil
}

func (s *Server) CreateDevice(ctx context.Context, rq oapi.CreateDeviceRequestObject) (oapi.CreateDeviceResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.require(v, p, auth.PermInventoryEdit, uuid.Nil); err != nil {
		return nil, err
	}
	id := newID()
	cmd := core.CreateDevice{Actor: p.UserID, Device: inventory.Element{ID: id, Label: rq.Body.Label, Notes: value(rq.Body.Notes)}}
	if v, err = s.do(ctx, cmd); err != nil {
		return nil, err
	}
	e, err := element(v, id)
	return oapi.CreateDevice201JSONResponse(e), err
}

func (s *Server) DeleteDevice(ctx context.Context, rq oapi.DeleteDeviceRequestObject) (oapi.DeleteDeviceResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if !s.visibleKind(v, p, rq.DeviceID, inventory.KindDevice) {
		return nil, errNotFound
	}
	if err := s.require(v, p, auth.PermInventoryEdit, uuid.Nil); err != nil {
		return nil, err
	}
	if _, err := s.do(ctx, core.DeleteDevice{Actor: p.UserID, ID: rq.DeviceID}); err != nil {
		return nil, inUse(err, "device_in_use", s.seesPresets(v, p))
	}
	return oapi.DeleteDevice204Response{}, nil
}

func (s *Server) ListSources(ctx context.Context, rq oapi.ListSourcesRequestObject) (oapi.ListSourcesResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if !s.visibleKind(v, p, rq.DeviceID, inventory.KindDevice) {
		return nil, errNotFound
	}
	out := oapi.ListSources200JSONResponse{}
	for _, src := range v.Graph.Sources() {
		if src.DeviceID == rq.DeviceID {
			out = append(out, toSource(src))
		}
	}
	slices.SortFunc(out, func(a, b oapi.Source) int { return strings.Compare(a.Driver, b.Driver) })
	return out, nil
}

func (s *Server) CreateSource(ctx context.Context, rq oapi.CreateSourceRequestObject) (oapi.CreateSourceResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if !s.visibleKind(v, p, rq.DeviceID, inventory.KindDevice) {
		return nil, errNotFound
	}
	if err := s.require(v, p, auth.PermSourceEdit, uuid.Nil); err != nil {
		return nil, err
	}
	b, id := rq.Body, newID()
	src := inventory.Source{ID: id, DeviceID: rq.DeviceID, Driver: b.Driver, Address: value(b.Address),
		Config: b.Config, SecretID: valueID(b.SecretID), Enabled: b.Enabled}
	if v, err = s.do(ctx, core.CreateSource{Actor: p.UserID, Source: src}); err != nil {
		return nil, err
	}
	created, ok := v.Graph.Source(id)
	if !ok {
		return nil, errNotFound
	}
	return oapi.CreateSource201JSONResponse(toSource(created)), nil
}

// source prüft, dass die Quelle existiert und ihr Zugang sichtbar ist.
func (s *Server) source(v *core.View, p auth.Principal, id uuid.UUID) (inventory.Source, error) {
	src, ok := v.Graph.Source(id)
	if !ok || !s.visible(v, p, src.AccessElementID) {
		return inventory.Source{}, errNotFound
	}
	return src, nil
}

func (s *Server) UpdateSource(ctx context.Context, rq oapi.UpdateSourceRequestObject) (oapi.UpdateSourceResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.source(v, p, rq.SourceID); err != nil {
		return nil, err
	}
	if err := s.require(v, p, auth.PermSourceEdit, uuid.Nil); err != nil {
		return nil, err
	}
	b := rq.Body
	src := inventory.Source{ID: rq.SourceID, Address: value(b.Address), Config: b.Config, SecretID: valueID(b.SecretID), Enabled: b.Enabled}
	if v, err = s.do(ctx, core.UpdateSource{Actor: p.UserID, Source: src}); err != nil {
		return nil, err
	}
	updated, ok := v.Graph.Source(rq.SourceID)
	if !ok {
		return nil, errNotFound
	}
	return oapi.UpdateSource200JSONResponse(toSource(updated)), nil
}

func (s *Server) DeleteSource(ctx context.Context, rq oapi.DeleteSourceRequestObject) (oapi.DeleteSourceResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.source(v, p, rq.SourceID); err != nil {
		return nil, err
	}
	if err := s.require(v, p, auth.PermSourceEdit, uuid.Nil); err != nil {
		return nil, err
	}
	if _, err := s.do(ctx, core.DeleteSource{Actor: p.UserID, ID: rq.SourceID}); err != nil {
		return nil, inUse(err, "source_in_use", s.seesPresets(v, p))
	}
	return oapi.DeleteSource204Response{}, nil
}

func (s *Server) CreateGroup(ctx context.Context, rq oapi.CreateGroupRequestObject) (oapi.CreateGroupResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.require(v, p, auth.PermInventoryEdit, uuid.Nil); err != nil {
		return nil, err
	}
	id := newID()
	cmd := core.CreateGroup{Actor: p.UserID, Group: inventory.Element{ID: id, Label: rq.Body.Label, Notes: value(rq.Body.Notes)}}
	if v, err = s.do(ctx, cmd); err != nil {
		return nil, err
	}
	e, err := element(v, id)
	return oapi.CreateGroup201JSONResponse(e), err
}

func (s *Server) DeleteGroup(ctx context.Context, rq oapi.DeleteGroupRequestObject) (oapi.DeleteGroupResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if !s.visibleKind(v, p, rq.GroupID, inventory.KindGroup) {
		return nil, errNotFound
	}
	if err := s.require(v, p, auth.PermInventoryEdit, uuid.Nil); err != nil {
		return nil, err
	}
	if _, err := s.do(ctx, core.DeleteGroup{Actor: p.UserID, ID: rq.GroupID}); err != nil {
		return nil, inUse(err, "group_in_use", s.seesPresets(v, p))
	}
	return oapi.DeleteGroup204Response{}, nil
}

// membership prüft Sichtbarkeit von Gruppe und Mitglied und das Recht inventory_edit an beiden.
func (s *Server) membership(v *core.View, p auth.Principal, group, member uuid.UUID) error {
	if !s.visibleKind(v, p, group, inventory.KindGroup) || !s.visible(v, p, member) {
		return errNotFound
	}
	if err := s.require(v, p, auth.PermInventoryEdit, group); err != nil {
		return err
	}
	return s.require(v, p, auth.PermInventoryEdit, member)
}

// memberLink liefert eine Mitgliedschaft nach Prüfung wie membership.
func (s *Server) memberLink(v *core.View, p auth.Principal, id uuid.UUID) (inventory.Link, error) {
	l, ok := v.Graph.Link(id)
	if !ok || l.Type != inventory.LinkMember {
		return inventory.Link{}, errNotFound
	}
	return l, s.membership(v, p, l.FromID, l.ToID)
}

func (s *Server) AddGroupMember(ctx context.Context, rq oapi.AddGroupMemberRequestObject) (oapi.AddGroupMemberResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	b := rq.Body
	if err := s.membership(v, p, rq.GroupID, b.ElementID); err != nil {
		return nil, err
	}
	id := newID()
	l := inventory.Link{ID: id, FromID: rq.GroupID, ToID: b.ElementID, Position: b.Position, Label: value(b.Label)}
	if v, err = s.do(ctx, core.AddGroupMember{Actor: p.UserID, Link: l}); err != nil {
		return nil, err
	}
	created, ok := v.Graph.Link(id)
	if !ok {
		return nil, errNotFound
	}
	return oapi.AddGroupMember201JSONResponse(toLink(created)), nil
}

func (s *Server) UpdateGroupMember(ctx context.Context, rq oapi.UpdateGroupMemberRequestObject) (oapi.UpdateGroupMemberResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.memberLink(v, p, rq.LinkID); err != nil {
		return nil, err
	}
	l := inventory.Link{ID: rq.LinkID, Position: rq.Body.Position, Label: value(rq.Body.Label)}
	if v, err = s.do(ctx, core.UpdateGroupMember{Actor: p.UserID, Link: l}); err != nil {
		return nil, err
	}
	updated, ok := v.Graph.Link(rq.LinkID)
	if !ok {
		return nil, errNotFound
	}
	return oapi.UpdateGroupMember200JSONResponse(toLink(updated)), nil
}

func (s *Server) RemoveGroupMember(ctx context.Context, rq oapi.RemoveGroupMemberRequestObject) (oapi.RemoveGroupMemberResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.memberLink(v, p, rq.LinkID); err != nil {
		return nil, err
	}
	if _, err := s.do(ctx, core.RemoveGroupMember{Actor: p.UserID, ID: rq.LinkID}); err != nil {
		return nil, err
	}
	return oapi.RemoveGroupMember204Response{}, nil
}

func (s *Server) UpdateUserFields(ctx context.Context, rq oapi.UpdateUserFieldsRequestObject) (oapi.UpdateUserFieldsResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if !s.visible(v, p, rq.ElementID) {
		return nil, errNotFound
	}
	if err := s.require(v, p, auth.PermInventoryEdit, rq.ElementID); err != nil {
		return nil, err
	}
	f := inventory.UserFields{Label: value(rq.Body.Label), Notes: value(rq.Body.Notes), Monitored: rq.Body.Monitored}
	if v, err = s.do(ctx, core.UpdateUserFields{Actor: p.UserID, ID: rq.ElementID, Fields: f}); err != nil {
		return nil, err
	}
	e, err := element(v, rq.ElementID)
	return oapi.UpdateUserFields200JSONResponse(e), err
}
