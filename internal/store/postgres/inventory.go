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
			Sources:  mapSlice(sources, toSource),
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

func (s *Store) CreateDevice(ctx context.Context, e inventory.Element) error {
	return s.q.CreateDevice(ctx, db.CreateDeviceParams{ID: e.ID, Label: nullable(e.Label), Notes: nullable(e.Notes)})
}

// DeleteDevice sperrt das Gerät und prüft die Presets in derselben Transaktion, damit
// zwischen Prüfung und Löschen kein Preset-Schritt entstehen kann. Danach werden die aktiven
// Befunde zurückgezogen, die Konfiguration mit diesem Geltungsbereich gelöscht und das Gerät
// weich gelöscht.
func (s *Store) DeleteDevice(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		deletedAt, err := q.LockDevice(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock device: %w", err)
		}
		if deletedAt != nil {
			return nil
		}

		presets, err := q.ListPresetsUsingDevice(ctx, id)
		if err != nil {
			return fmt.Errorf("list presets using device: %w", err)
		}
		if len(presets) > 0 {
			return &inventory.DeviceInUseError{Presets: mapSlice(presets, func(p db.ListPresetsUsingDeviceRow) inventory.PresetRef {
				return inventory.PresetRef{ID: p.ID, Name: p.Name}
			})}
		}

		if _, err := q.WithdrawDeviceFindings(ctx, id); err != nil {
			return fmt.Errorf("withdraw findings: %w", err)
		}
		if err := q.DeleteDeviceScopedConfig(ctx, id); err != nil {
			return fmt.Errorf("delete scoped config: %w", err)
		}
		if err := q.DeleteDevice(ctx, id); err != nil {
			return fmt.Errorf("delete device: %w", err)
		}
		return nil
	})
}

func (s *Store) CreateSource(ctx context.Context, src inventory.Source, accessID uuid.UUID) error {
	n, err := s.q.CreateDeviceSource(ctx, db.CreateDeviceSourceParams{
		ID:       src.ID,
		AccessID: accessID,
		DeviceID: src.DeviceID,
		Driver:   src.Driver,
		Address:  nullable(src.Address),
		Config:   jsonObject(src.Config),
		SecretID: nullable(src.SecretID),
		Enabled:  src.Enabled,
	})
	return rowsAffected(n, err)
}

func (s *Store) UpdateSource(ctx context.Context, src inventory.Source) error {
	n, err := s.q.UpdateDeviceSource(ctx, db.UpdateDeviceSourceParams{
		ID:       src.ID,
		Address:  nullable(src.Address),
		Config:   jsonObject(src.Config),
		SecretID: nullable(src.SecretID),
		Enabled:  src.Enabled,
	})
	return rowsAffected(n, err)
}

func (s *Store) CreateGroup(ctx context.Context, e inventory.Element) error {
	return s.q.CreateGroup(ctx, db.CreateGroupParams{ID: e.ID, Label: nullable(e.Label), Notes: nullable(e.Notes)})
}

func (s *Store) CreatePlannedElement(ctx context.Context, e inventory.Element) error {
	return s.q.CreatePlannedElement(ctx, db.CreatePlannedElementParams{
		ID:         e.ID,
		Kind:       string(e.Kind),
		ParentID:   e.ParentID,
		DeviceID:   e.DeviceID,
		SourceID:   e.SourceID,
		ExternalID: e.ExternalID,
		Label:      nullable(e.Label),
		Notes:      nullable(e.Notes),
	})
}

func (s *Store) UpdateUserFields(ctx context.Context, id uuid.UUID, f inventory.UserFields) error {
	n, err := s.q.UpdateElementUserFields(ctx, db.UpdateElementUserFieldsParams{
		ID:        id,
		Label:     nullable(f.Label),
		Notes:     nullable(f.Notes),
		Monitored: f.Monitored,
	})
	return rowsAffected(n, err)
}

// AddGroupMember nimmt Lock, Zyklenprüfung und Insert in einer Transaktion vor, damit
// parallele Änderungen keinen Zyklus erzeugen können.
func (s *Store) AddGroupMember(ctx context.Context, l inventory.Link) error {
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

func (s *Store) UpdateGroupMember(ctx context.Context, l inventory.Link) error {
	n, err := s.q.UpdateMemberLink(ctx, db.UpdateMemberLinkParams{
		ID:       l.ID,
		Position: convert(l.Position, toInt32),
		Label:    nullable(l.Label),
	})
	return rowsAffected(n, err)
}

func (s *Store) RemoveGroupMember(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteMemberLink(ctx, id)
	return rowsAffected(n, err)
}

// rowsAffected meldet ErrNotFound, wenn eine Änderung keine Zeile getroffen hat.
func rowsAffected(n int64, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return inventory.ErrNotFound
	}
	return nil
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
		Present:         e.Present,
		FirstSeenAt:     value(e.FirstSeenAt),
		LastSeenAt:      value(e.LastSeenAt),
		Label:           value(e.Label),
		Notes:           value(e.Notes),
		Monitored:       e.Monitored,
		DeletedAt:       value(e.DeletedAt),
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
	}
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
