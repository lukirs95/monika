package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/lukirs95/monika/internal/preset"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

func (s *Store) ListPresets(ctx context.Context) ([]preset.Preset, error) {
	rows, err := s.q.ListPresets(ctx)
	if err != nil {
		return nil, err
	}
	return mapSlice(rows, func(r db.ListPresetsRow) preset.Preset {
		return preset.Preset{ID: r.ID, Name: r.Name, Description: value(r.Description), CreatedByID: value(r.CreatedByID)}
	}), nil
}

func (s *Store) GetPreset(ctx context.Context, id uuid.UUID) (preset.Preset, error) {
	var p preset.Preset
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, s.pool, opts, func(tx pgx.Tx) error {
		q := db.New(tx)
		r, err := q.GetPreset(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return preset.ErrNotFound
		}
		if err != nil {
			return err
		}
		steps, err := q.ListPresetSteps(ctx, id)
		if err != nil {
			return err
		}
		p = preset.Preset{
			ID: r.ID, Name: r.Name, Description: value(r.Description), CreatedByID: value(r.CreatedByID),
			Steps: mapSlice(steps, toStep),
		}
		return nil
	})
	return p, err
}

func (s *Store) CreatePreset(ctx context.Context, actor uuid.UUID, p preset.Preset) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.CreatePresetRow(ctx, db.CreatePresetRowParams{
			ActorID: nullable(actor), ID: p.ID, Name: p.Name, Description: nullable(p.Description),
		}); err != nil {
			return err
		}
		if err := saveSteps(ctx, q, p); err != nil {
			return err
		}
		return auditPreset(ctx, q, actor, "create", p.ID, nil)
	})
}

func (s *Store) UpdatePreset(ctx context.Context, actor uuid.UUID, p preset.Preset) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		before, err := q.PresetSnapshot(ctx, p.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return preset.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := q.UpdatePresetRow(ctx, db.UpdatePresetRowParams{
			ID: p.ID, Name: p.Name, Description: nullable(p.Description),
		}); err != nil {
			return err
		}
		if err := q.DeleteRemovedPresetSteps(ctx, db.DeleteRemovedPresetStepsParams{
			PresetID: p.ID, KeepIds: mapSlice(p.Steps, func(st preset.Step) uuid.UUID { return st.ID }),
		}); err != nil {
			return err
		}
		if err := saveSteps(ctx, q, p); err != nil {
			return err
		}
		return auditPreset(ctx, q, actor, "update", p.ID, before)
	})
}

func (s *Store) DeletePreset(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		before, err := q.PresetSnapshot(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return preset.ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := q.DeletePresetRow(ctx, id); err != nil {
			return err
		}
		return q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
			ActorID: nullable(actor), Verb: "delete", ObjectType: "preset", ObjectID: id, Before: before,
		})
	})
}

// saveSteps prüft und sperrt die Ziele und schreibt die Schritte im Batch.
func saveSteps(ctx context.Context, q *db.Queries, p preset.Preset) error {
	if len(p.Steps) == 0 {
		return nil
	}
	if err := checkTargets(ctx, q, p.Steps); err != nil {
		return err
	}
	params := mapSlice(p.Steps, func(st preset.Step) db.UpsertPresetStepParams {
		return db.UpsertPresetStepParams{
			ID: st.ID, PresetID: p.ID, Stage: int32(st.Stage), Position: int32(st.Position),
			DelayMs: int32(st.Delay / time.Millisecond), ElementID: st.ElementID, Action: st.Action,
			Params: st.Params, ContinueOnError: st.ContinueOnError, Driver: nullable(st.Driver),
		}
	})
	var err error
	q.UpsertPresetStep(ctx, params).QueryRow(func(i int, _ uuid.UUID, e error) {
		switch {
		case err != nil:
		case errors.Is(e, pgx.ErrNoRows):
			err = fmt.Errorf("%w: step %s belongs to another preset", preset.ErrInvalidTarget, params[i].ID)
		case e != nil:
			err = fmt.Errorf("save step %s: %w", params[i].ID, e)
		}
	})
	return err
}

// checkTargets sperrt Ziele und Geräte gegen gleichzeitiges Löschen und lehnt unbekannte oder
// gelöschte Ziele sowie Elemente gelöschter Geräte ab.
func checkTargets(ctx context.Context, q *db.Queries, steps []preset.Step) error {
	rows, err := q.LockStepTargets(ctx, mapSlice(steps, func(st preset.Step) uuid.UUID { return st.ElementID }))
	if err != nil {
		return fmt.Errorf("lock step targets: %w", err)
	}
	locked := make(map[uuid.UUID]db.LockStepTargetsRow, len(rows))
	for _, r := range rows {
		locked[r.ID] = r
	}
	for _, st := range steps {
		t, ok := locked[st.ElementID]
		if !ok || t.DeletedAt != nil {
			return fmt.Errorf("%w: target %s is unknown or deleted", preset.ErrInvalidTarget, st.ElementID)
		}
		if t.DeviceID != nil && locked[*t.DeviceID].DeletedAt != nil {
			return fmt.Errorf("%w: target %s belongs to a deleted device", preset.ErrInvalidTarget, st.ElementID)
		}
	}
	return nil
}

// auditPreset schreibt den Audit-Eintrag mit dem Stand nach der Änderung, am Ende der Transaktion.
func auditPreset(ctx context.Context, q *db.Queries, actor uuid.UUID, verb string, id uuid.UUID, before []byte) error {
	after, err := q.PresetSnapshot(ctx, id)
	if err != nil {
		return err
	}
	return q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		ActorID: nullable(actor), Verb: verb, ObjectType: "preset", ObjectID: id, Before: before, After: after,
	})
}

func toStep(st db.PresetStep) preset.Step {
	return preset.Step{
		ID: st.ID, Stage: int(st.Stage), Position: int(st.Position),
		Delay:     time.Duration(st.DelayMs) * time.Millisecond,
		ElementID: st.ElementID, Action: st.Action, Params: st.Params,
		ContinueOnError: st.ContinueOnError, Driver: value(st.Driver),
	}
}
