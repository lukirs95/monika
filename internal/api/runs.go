package api

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Läufe (docs/contracts/flows.md, Aktion ausführen). Laufende Läufe stehen in der View, beendete
// im Verlauf.

func (s *Server) SubmitRun(ctx context.Context, rq oapi.SubmitRunRequestObject) (oapi.SubmitRunResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	b := rq.Body
	if !s.visible(v, p, b.ElementID) {
		return nil, errNotFound
	}
	req := executor.Request{Actor: p.UserID, ElementID: b.ElementID, Action: b.Action, Params: b.Params,
		DeviceLevel: value(b.DeviceLevel), Driver: value(b.Driver)}
	// Rechte an jedem aufgelösten Ziel, wie beim Start eines Presets; critical hängt am Ziel.
	targets, err := s.cfg.Executor.Targets(v, req)
	if err != nil {
		return nil, err
	}
	perm := auth.PermOperate
	if b.Action == vocab.ActionSet {
		perm = auth.PermConfigure
	}
	ids := make([]uuid.UUID, 0, len(targets))
	for _, t := range targets {
		if err := s.require(v, p, perm, t.ElementID); err != nil {
			return nil, err
		}
		if t.Critical {
			if err := s.require(v, p, auth.PermCritical, t.ElementID); err != nil {
				return nil, err
			}
		}
		ids = append(ids, t.ElementID)
	}
	// Eine Sammelaktion bekommt genau die geprüften Ziele; was inzwischen dazukommt, läuft nicht
	// ungeprüft mit.
	if len(ids) != 1 || ids[0] != b.ElementID {
		req.Targets = ids
	}
	id, err := s.cfg.Executor.Submit(ctx, req)
	if err != nil {
		return nil, err
	}
	return oapi.SubmitRun202JSONResponse{RunID: id}, nil
}

// run liefert einen laufenden Lauf, den der Benutzer sieht.
func (s *Server) run(v *core.View, p auth.Principal, id uuid.UUID) (action.Run, error) {
	r, ok := v.Runs[id]
	if !ok || !s.seesRun(v, p, r) {
		return action.Run{}, errNotFound
	}
	return r, nil
}

// seesRun: sein Ziel ist sichtbar; Wurzeln von Presets mit preset_run oder preset_edit.
func (s *Server) seesRun(v *core.View, p auth.Principal, r action.Run) bool {
	if r.ElementID == uuid.Nil {
		return s.seesPresets(v, p)
	}
	return s.visible(v, p, r.ElementID)
}

func (s *Server) GetRun(ctx context.Context, rq oapi.GetRunRequestObject) (oapi.GetRunResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.run(v, p, rq.RunID)
	if errors.Is(err, errNotFound) {
		stored, children, err := s.storedRun(ctx, v, p, rq.RunID)
		if err != nil {
			return nil, err
		}
		out := toRun(stored, children)
		out.ElementDeleted = deleted(v, stored.ElementID)
		return oapi.GetRun200JSONResponse(out), nil
	} else if err != nil {
		return nil, err
	}
	// Kinder aus dem Verlauf (auch beendete), laufende mit ihrem Stand aus der View.
	_, stored, err := s.cfg.History.RunByID(ctx, r.ID)
	if err != nil && !errors.Is(err, action.ErrNotFound) {
		return nil, err
	}
	children := map[uuid.UUID]action.Run{}
	for _, c := range stored {
		children[c.ID] = c
	}
	for _, c := range v.Runs {
		if c.ParentID == r.ID {
			children[c.ID] = c
		}
	}
	return oapi.GetRun200JSONResponse(toRun(r, slices.Collect(maps.Values(children)))), nil
}

func (s *Server) CancelRun(ctx context.Context, rq oapi.CancelRunRequestObject) (oapi.CancelRunResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.run(v, p, rq.RunID)
	if errors.Is(err, errNotFound) {
		// Nicht mehr in der View: beendet (409), wenn der Benutzer ihn sieht, sonst unbekannt.
		if _, _, err := s.storedRun(ctx, v, p, rq.RunID); err != nil {
			return nil, err
		}
		return nil, executor.ErrFinished
	} else if err != nil {
		return nil, err
	}
	root := r.Action == executor.ActionPreset && r.ParentID == uuid.Nil
	// Abbrechen darf, wer ihn ausgelöst hat, sonst operate am Ziel bzw. preset_run beim Preset.
	if r.RequestedByID != p.UserID {
		perm, at := auth.PermOperate, r.ElementID
		if root {
			perm, at = auth.PermPresetRun, uuid.Nil
		}
		if err := s.require(v, p, perm, at); err != nil {
			return nil, err
		}
	}
	if root {
		err = s.cfg.Presets.Cancel(ctx, r.ID)
	} else {
		err = s.cfg.Executor.Cancel(ctx, []uuid.UUID{r.ID})
	}
	if err != nil {
		return nil, err
	}
	return oapi.CancelRun204Response{}, nil
}
