package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) Load(ctx context.Context) (inventory.Snapshot, error) {
	var snap inventory.Snapshot
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, s.pool, opts, func(tx pgx.Tx) error {
		q := db.New(tx)
		sources, err := q.ListDeviceSources(ctx)
		if err != nil {
			return fmt.Errorf("list sources: %w", err)
		}
		elements, err := q.ListElements(ctx)
		if err != nil {
			return fmt.Errorf("list elements: %w", err)
		}
		links, err := q.ListElementLinks(ctx)
		if err != nil {
			return fmt.Errorf("list links: %w", err)
		}
		snap = inventory.Snapshot{
			Sources:  mapSlice(sources, toSourceRow),
			Elements: mapSlice(elements, toElement),
			Links:    mapSlice(links, toLink),
		}
		return nil
	})
	return snap, err
}

func (s *Store) ApplyDiscovery(ctx context.Context, d inventory.Discovery) ([]uuid.UUID, error) {
	var missing []uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if id := d.Identity; id != nil {
			if _, err := q.UpdateSourceIdentity(ctx, db.UpdateSourceIdentityParams{
				ID:       d.SourceID,
				Vendor:   nullable(id.Vendor),
				Model:    nullable(id.Model),
				Serial:   nullable(id.Serial),
				Firmware: nullable(id.Firmware),
			}); err != nil {
				return fmt.Errorf("update source identity: %w", err)
			}
		}
		if err := upsertElements(ctx, q, d.Upsert); err != nil {
			return err
		}
		if d.GlobalRef != nil {
			if _, err := q.SetAccessGlobalRef(ctx, db.SetAccessGlobalRefParams{SourceID: d.SourceID, GlobalRef: nullable(*d.GlobalRef)}); err != nil {
				return fmt.Errorf("set access global_ref: %w", err)
			}
			if *d.GlobalRef != "" {
				if err := resolveAccessRef(ctx, q, d.SourceID, *d.GlobalRef); err != nil {
					return err
				}
			}
		}
		// Erst löschen, dann einfügen: ein ersetzter Link kann dieselben Endpunkte haben.
		if len(d.RemoveLinks) > 0 {
			if _, err := q.DeleteDriverLinks(ctx, db.DeleteDriverLinksParams{SourceID: d.SourceID, Ids: d.RemoveLinks}); err != nil {
				return fmt.Errorf("delete links: %w", err)
			}
		}
		if err := insertLinks(ctx, q, d.AddLinks); err != nil {
			return err
		}
		for _, e := range d.Upsert {
			if e.GlobalRef == "" {
				continue
			}
			if _, err := q.ResolveLinkRef(ctx, db.ResolveLinkRefParams{GlobalRef: e.GlobalRef, ElementID: e.ID}); err != nil {
				return fmt.Errorf("resolve links to %s: %w", e.GlobalRef, err)
			}
		}
		var err error
		missing, err = q.FinishDiscovery(ctx, db.FinishDiscoveryParams{SourceID: d.SourceID, SeenIds: d.Seen})
		if err != nil {
			return fmt.Errorf("finish discovery: %w", err)
		}
		return nil
	})
	return missing, err
}

// upsertElements schreibt Treiber-Elemente in einem Batch. Die gespeicherte ID muss der
// übergebenen entsprechen, sonst weicht der Stand im Core von der Datenbank ab.
func upsertElements(ctx context.Context, q *db.Queries, elements []inventory.Element) error {
	if len(elements) == 0 {
		return nil
	}
	params := mapSlice(elements, toUpsertParams)
	var err error
	q.UpsertDriverElement(ctx, params).QueryRow(func(i int, id uuid.UUID, e error) {
		p := params[i]
		switch {
		case err != nil:
		case e != nil:
			err = fmt.Errorf("upsert element %q: %w", p.ExternalID, e)
		case id != p.ID:
			err = fmt.Errorf("element %q is stored as %s, not %s", p.ExternalID, id, p.ID)
		}
	})
	return err
}

func insertLinks(ctx context.Context, q *db.Queries, links []inventory.Link) error {
	if len(links) == 0 {
		return nil
	}
	params := mapSlice(links, func(l inventory.Link) db.InsertDriverLinkParams {
		return db.InsertDriverLinkParams{
			ID:       l.ID,
			Type:     db.LinkType(l.Type),
			SourceID: l.SourceID,
			FromID:   nullable(l.FromID),
			FromRef:  nullable(l.FromRef),
			ToID:     nullable(l.ToID),
			ToRef:    nullable(l.ToRef),
		}
	})
	var err error
	q.InsertDriverLink(ctx, params).Exec(func(i int, e error) {
		if err == nil && e != nil {
			err = fmt.Errorf("insert link %s: %w", params[i].ID, e)
		}
	})
	return err
}

func (s *Store) CreateDevice(ctx context.Context, actor uuid.UUID, e inventory.Element) error {
	return s.q.CreateDevice(ctx, db.CreateDeviceParams{ActorID: nullable(actor), ID: e.ID, Label: nullable(e.Label), Notes: nullable(e.Notes)})
}

// DeleteDevice sperrt das Gerät und prüft die Presets in derselben Transaktion, damit
// zwischen Prüfung und Löschen kein Preset-Schritt entstehen kann. Danach werden die aktiven
// Befunde zurückgezogen, die Konfiguration an seinen Elementen mit Audit gelöscht und das
// Gerät hart gelöscht; Quellen, Elemente und Links folgen per CASCADE.
func (s *Store) DeleteDevice(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := q.LockDevice(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock device: %w", err)
		}
		presets, err := q.ListPresetsUsingDevice(ctx, id)
		if err != nil {
			return fmt.Errorf("list presets using device: %w", err)
		}
		if len(presets) > 0 {
			return inUse(presets, func(p db.ListPresetsUsingDeviceRow) (uuid.UUID, string) { return p.ID, p.Name })
		}
		ids, err := q.ListDeviceElementIDs(ctx, id)
		if err != nil {
			return fmt.Errorf("list device elements: %w", err)
		}
		if err := clearElements(ctx, q, actor, ids); err != nil {
			return err
		}
		if err := q.DeleteDevice(ctx, db.DeleteDeviceParams{ActorID: nullable(actor), DeviceID: id}); err != nil {
			return fmt.Errorf("delete device: %w", err)
		}
		return nil
	})
}

// DeleteSource arbeitet wie DeleteDevice, für die Elemente einer Quelle.
func (s *Store) DeleteSource(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := q.LockSource(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock source: %w", err)
		}
		presets, err := q.ListPresetsUsingSource(ctx, id)
		if err != nil {
			return fmt.Errorf("list presets using source: %w", err)
		}
		if len(presets) > 0 {
			return inUse(presets, func(p db.ListPresetsUsingSourceRow) (uuid.UUID, string) { return p.ID, p.Name })
		}
		ids, err := q.ListSourceElementIDs(ctx, id)
		if err != nil {
			return fmt.Errorf("list source elements: %w", err)
		}
		if err := clearElements(ctx, q, actor, ids); err != nil {
			return err
		}
		if err := q.DeleteSource(ctx, db.DeleteSourceParams{ActorID: nullable(actor), SourceID: id}); err != nil {
			return fmt.Errorf("delete source: %w", err)
		}
		return nil
	})
}

// DeleteGroup arbeitet wie DeleteDevice: Sperre, Prüfung der Presets, Befunde der Regeln mit
// diesem Geltungsbereich zurückziehen, Konfiguration und Mitgliedschaften mit Audit löschen,
// dann die Gruppe hart löschen.
func (s *Store) DeleteGroup(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := q.LockGroup(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock group: %w", err)
		}
		presets, err := q.ListPresetsUsingGroup(ctx, id)
		if err != nil {
			return fmt.Errorf("list presets using group: %w", err)
		}
		if len(presets) > 0 {
			return inUse(presets, func(p db.ListPresetsUsingGroupRow) (uuid.UUID, string) { return p.ID, p.Name })
		}
		if _, err := q.WithdrawGroupRuleFindings(ctx, id); err != nil {
			return fmt.Errorf("withdraw rule findings: %w", err)
		}
		if err := q.DeleteScopedConfig(ctx, db.DeleteScopedConfigParams{ActorID: nullable(actor), ElementIds: []uuid.UUID{id}}); err != nil {
			return fmt.Errorf("delete scoped config: %w", err)
		}
		if err := q.DeleteGroup(ctx, db.DeleteGroupParams{ActorID: nullable(actor), GroupID: id}); err != nil {
			return fmt.Errorf("delete group: %w", err)
		}
		return nil
	})
}

// clearElements zieht die aktiven Befunde an den Elementen zurück und löscht die Konfiguration,
// die an ihnen hängt, mit Audit; danach können die Elemente hart gelöscht werden.
func clearElements(ctx context.Context, q *db.Queries, actor uuid.UUID, ids []uuid.UUID) error {
	if _, err := q.WithdrawFindings(ctx, ids); err != nil {
		return fmt.Errorf("withdraw findings: %w", err)
	}
	if err := q.DeleteScopedConfig(ctx, db.DeleteScopedConfigParams{ActorID: nullable(actor), ElementIds: ids}); err != nil {
		return fmt.Errorf("delete scoped config: %w", err)
	}
	return nil
}

func inUse[T any](rows []T, ref func(T) (uuid.UUID, string)) error {
	return &inventory.InUseError{Presets: mapSlice(rows, func(r T) inventory.PresetRef {
		id, name := ref(r)
		return inventory.PresetRef{ID: id, Name: name}
	})}
}

// resolveAccessRef löst unaufgelöste Links auf das Zugangs-Element der Quelle auf.
func resolveAccessRef(ctx context.Context, q *db.Queries, sourceID uuid.UUID, globalRef string) error {
	accessID, err := q.AccessElementID(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("access element of source %s: %w", sourceID, err)
	}
	if _, err := q.ResolveLinkRef(ctx, db.ResolveLinkRefParams{GlobalRef: globalRef, ElementID: accessID}); err != nil {
		return fmt.Errorf("resolve links to %s: %w", globalRef, err)
	}
	return nil
}

func (s *Store) CreateSource(ctx context.Context, actor uuid.UUID, src inventory.Source, accessID uuid.UUID) error {
	n, err := s.q.CreateDeviceSource(ctx, db.CreateDeviceSourceParams{
		ActorID:  nullable(actor),
		ID:       src.ID,
		AccessID: accessID,
		DeviceID: src.DeviceID,
		Driver:   src.Driver,
		Address:  nullable(src.Address),
		Config:   jsonObject(src.Config),
		SecretID: nullable(src.SecretID),
		Enabled:  src.Enabled,
	})
	return affected(n, err, inventory.ErrNotFound)
}

func (s *Store) UpdateSource(ctx context.Context, actor uuid.UUID, src inventory.Source) error {
	n, err := s.q.UpdateDeviceSource(ctx, db.UpdateDeviceSourceParams{
		ActorID:  nullable(actor),
		ID:       src.ID,
		Address:  nullable(src.Address),
		Config:   jsonObject(src.Config),
		SecretID: nullable(src.SecretID),
		Enabled:  src.Enabled,
	})
	return affected(n, err, inventory.ErrNotFound)
}

func (s *Store) CreateGroup(ctx context.Context, actor uuid.UUID, e inventory.Element) error {
	return s.q.CreateGroup(ctx, db.CreateGroupParams{ActorID: nullable(actor), ID: e.ID, Label: nullable(e.Label), Notes: nullable(e.Notes)})
}

func (s *Store) UpdateUserFields(ctx context.Context, actor uuid.UUID, id uuid.UUID, f inventory.UserFields) error {
	n, err := s.q.UpdateElementUserFields(ctx, db.UpdateElementUserFieldsParams{
		ActorID:   nullable(actor),
		ID:        id,
		Label:     nullable(f.Label),
		Notes:     nullable(f.Notes),
		Monitored: f.Monitored,
	})
	return affected(n, err, inventory.ErrNotFound)
}

// AddGroupMember nimmt Lock, Zyklenprüfung und Insert in einer Transaktion vor, damit
// parallele Änderungen keinen Zyklus erzeugen können.
func (s *Store) AddGroupMember(ctx context.Context, actor uuid.UUID, l inventory.Link) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.LockMemberLinks(ctx); err != nil {
			return fmt.Errorf("lock member links: %w", err)
		}
		cycle, err := q.MemberLinkWouldCycle(ctx, db.MemberLinkWouldCycleParams{GroupID: l.FromID, MemberID: l.ToID})
		if err != nil {
			return fmt.Errorf("check member cycle: %w", err)
		}
		if cycle {
			return inventory.ErrMemberCycle
		}
		if err := q.CreateMemberLink(ctx, db.CreateMemberLinkParams{
			ActorID:  nullable(actor),
			ID:       l.ID,
			GroupID:  l.FromID,
			MemberID: l.ToID,
			Position: convert(l.Position, toInt32),
			Label:    nullable(l.Label),
		}); err != nil {
			return fmt.Errorf("create member link: %w", err)
		}
		return nil
	})
}

func (s *Store) UpdateGroupMember(ctx context.Context, actor uuid.UUID, l inventory.Link) error {
	n, err := s.q.UpdateMemberLink(ctx, db.UpdateMemberLinkParams{
		ActorID:  nullable(actor),
		ID:       l.ID,
		Position: convert(l.Position, toInt32),
		Label:    nullable(l.Label),
	})
	return affected(n, err, inventory.ErrNotFound)
}

func (s *Store) RemoveGroupMember(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	n, err := s.q.DeleteMemberLink(ctx, db.DeleteMemberLinkParams{ActorID: nullable(actor), ID: id})
	return affected(n, err, inventory.ErrNotFound)
}

func toElement(e db.Element) inventory.Element {
	return inventory.Element{
		ID:              e.ID,
		Kind:            inventory.Kind(e.Kind),
		ParentID:        value(e.ParentID),
		DeviceID:        value(e.DeviceID),
		SourceID:        value(e.SourceID),
		ExternalID:      value(e.ExternalID),
		GlobalRef:       value(e.GlobalRef),
		Transport:       value(e.Transport),
		Direction:       inventory.Direction(value(e.Direction)),
		Name:            value(e.Name),
		Attributes:      e.Attributes,
		Position:        convert(e.Position, toInt),
		Actions:         e.Actions,
		CriticalActions: e.CriticalActions,
		Settings:        e.Settings,
		Params:          e.Params,
		Present:         e.Present,
		FirstSeenAt:     value(e.FirstSeenAt),
		LastSeenAt:      value(e.LastSeenAt),
		Label:           value(e.Label),
		Notes:           value(e.Notes),
		Monitored:       e.Monitored,
	}
}

func toUpsertParams(e inventory.Element) db.UpsertDriverElementParams {
	return db.UpsertDriverElementParams{
		ID:              e.ID,
		Kind:            string(e.Kind),
		ParentID:        e.ParentID,
		DeviceID:        e.DeviceID,
		SourceID:        e.SourceID,
		ExternalID:      e.ExternalID,
		GlobalRef:       nullable(e.GlobalRef),
		Transport:       nullable(e.Transport),
		Direction:       nullable(db.PortDirection(e.Direction)),
		Name:            nullable(e.Name),
		Attributes:      jsonObject(e.Attributes),
		Position:        convert(e.Position, toInt32),
		Actions:         nonNil(e.Actions),
		CriticalActions: nonNil(e.CriticalActions),
		Settings:        e.Settings,
		Params:          e.Params,
	}
}

func toSourceRow(r db.ListDeviceSourcesRow) inventory.Source {
	s := toSource(r.DeviceSource)
	s.AccessElementID = r.AccessElementID
	return s
}

func toSource(s db.DeviceSource) inventory.Source {
	return inventory.Source{
		ID:       s.ID,
		DeviceID: s.DeviceID,
		Driver:   s.Driver,
		Address:  value(s.Address),
		Config:   s.Config,
		SecretID: value(s.SecretID),
		Enabled:  s.Enabled,
		Identity: inventory.Identity{
			Vendor:   value(s.Vendor),
			Model:    value(s.Model),
			Serial:   value(s.Serial),
			Firmware: value(s.Firmware),
		},
	}
}

func toLink(l db.ElementLink) inventory.Link {
	return inventory.Link{
		ID:       l.ID,
		Type:     inventory.LinkType(l.Type),
		SourceID: value(l.SourceID),
		FromID:   value(l.FromID),
		FromRef:  value(l.FromRef),
		ToID:     value(l.ToID),
		ToRef:    value(l.ToRef),
		Position: convert(l.Position, toInt),
		Label:    value(l.Label),
	}
}

func toInt(v int32) int   { return int(v) }
func toInt32(v int) int32 { return int32(v) }
