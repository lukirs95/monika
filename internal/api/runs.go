package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Läufe (docs/contracts/flows.md, Aktion ausführen). Laufende Läufe stehen in der View; beendete
// kommen mit dem Verlauf (Schritt 11h).

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
	if err != nil {
		return nil, err
	}
	var children []action.Run
	for _, c := range v.Runs {
		if c.ParentID == r.ID {
			children = append(children, c)
		}
	}
	return oapi.GetRun200JSONResponse(toRun(r, children)), nil
}

func (s *Server) CancelRun(ctx context.Context, rq oapi.CancelRunRequestObject) (oapi.CancelRunResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.run(v, p, rq.RunID)
	if err != nil {
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
