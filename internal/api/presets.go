package api

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/preset"
)

// Presets: lesen mit preset_run oder preset_edit, ändern mit preset_edit, starten mit
// preset_run und den Rechten an allen Zielen (preset.Runner.Start).

func (s *Server) requirePresets(ctx context.Context) (auth.Principal, error) {
	return s.requireGlobal(ctx, auth.PermPresetRun, auth.PermPresetEdit)
}

func (s *Server) requirePresetEdit(ctx context.Context) (auth.Principal, error) {
	return s.requireGlobal(ctx, auth.PermPresetEdit)
}

func (s *Server) ListPresets(ctx context.Context, _ oapi.ListPresetsRequestObject) (oapi.ListPresetsResponseObject, error) {
	if _, err := s.requirePresets(ctx); err != nil {
		return nil, err
	}
	presets, err := s.cfg.Presets.List(ctx)
	if err != nil {
		return nil, err
	}
	out := oapi.ListPresets200JSONResponse{}
	for _, p := range presets {
		out = append(out, oapi.PresetSummary{ID: p.ID, Name: p.Name, Description: optional(p.Description), CreatedByID: optionalID(p.CreatedByID)})
	}
	return out, nil
}

func (s *Server) GetPreset(ctx context.Context, rq oapi.GetPresetRequestObject) (oapi.GetPresetResponseObject, error) {
	if _, err := s.requirePresets(ctx); err != nil {
		return nil, err
	}
	p, err := s.cfg.Presets.Get(ctx, rq.PresetID)
	if err != nil {
		return nil, err
	}
	return oapi.GetPreset200JSONResponse(toPreset(p)), nil
}

func (s *Server) CreatePreset(ctx context.Context, rq oapi.CreatePresetRequestObject) (oapi.CreatePresetResponseObject, error) {
	actor, err := s.requirePresetEdit(ctx)
	if err != nil {
		return nil, err
	}
	p := fromPresetInput(newID(), *rq.Body)
	if err := s.cfg.Presets.CreatePreset(ctx, actor.UserID, p); err != nil {
		return nil, err
	}
	created, err := s.cfg.Presets.Get(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return oapi.CreatePreset201JSONResponse(toPreset(created)), nil
}

func (s *Server) UpdatePreset(ctx context.Context, rq oapi.UpdatePresetRequestObject) (oapi.UpdatePresetResponseObject, error) {
	actor, err := s.requirePresetEdit(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Presets.UpdatePreset(ctx, actor.UserID, fromPresetInput(rq.PresetID, *rq.Body)); err != nil {
		return nil, err
	}
	updated, err := s.cfg.Presets.Get(ctx, rq.PresetID)
	if err != nil {
		return nil, err
	}
	return oapi.UpdatePreset200JSONResponse(toPreset(updated)), nil
}

func (s *Server) DeletePreset(ctx context.Context, rq oapi.DeletePresetRequestObject) (oapi.DeletePresetResponseObject, error) {
	actor, err := s.requirePresetEdit(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.cfg.Presets.DeletePreset(ctx, actor.UserID, rq.PresetID); err != nil {
		return nil, err
	}
	return oapi.DeletePreset204Response{}, nil
}

func (s *Server) RunPreset(ctx context.Context, rq oapi.RunPresetRequestObject) (oapi.RunPresetResponseObject, error) {
	v, p, err := s.viewFor(ctx)
	if err != nil {
		return nil, err
	}
	if !s.cfg.Auth.Allowed(v, p, auth.PermPresetRun, uuid.Nil) {
		return nil, missingRights(&preset.MissingRightsError{Missing: []preset.Missing{{Permission: auth.PermPresetRun}}})
	}
	root, err := s.cfg.Presets.Start(ctx, p, rq.PresetID)
	if err != nil {
		return nil, missingRights(err)
	}
	return oapi.RunPreset202JSONResponse{RunID: root}, nil
}

func fromPresetInput(id uuid.UUID, in oapi.PresetInput) preset.Preset {
	p := preset.Preset{ID: id, Name: in.Name, Description: value(in.Description)}
	for _, st := range in.Steps {
		p.Steps = append(p.Steps, preset.Step{
			ID: valueID(st.ID), Stage: st.Stage, Position: st.Position, Delay: time.Duration(st.DelayMs) * time.Millisecond,
			ElementID: st.ElementID, Action: st.Action, Params: st.Params, ContinueOnError: st.ContinueOnError,
			DeviceLevel: value(st.DeviceLevel), Driver: value(st.Driver),
		})
	}
	return p
}

func toPreset(p preset.Preset) oapi.Preset {
	out := oapi.Preset{ID: p.ID, Name: p.Name, Description: optional(p.Description), CreatedByID: optionalID(p.CreatedByID), Steps: []oapi.PresetStep{}}
	for _, st := range p.Steps {
		out.Steps = append(out.Steps, oapi.PresetStep{
			ID: optionalID(st.ID), Stage: st.Stage, Position: st.Position, DelayMs: int(st.Delay / time.Millisecond),
			ElementID: st.ElementID, Action: st.Action, Params: st.Params, ContinueOnError: st.ContinueOnError,
			DeviceLevel: new(st.DeviceLevel), Driver: optional(st.Driver),
		})
	}
	return out
}
