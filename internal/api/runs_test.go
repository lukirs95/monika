package api

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/preset"
	"github.com/lukirs95/monika/sdk/vocab"
)

// fakeExecutor löst Ziele nach einer Tabelle auf; reboot ist überall kritisch.
type fakeExecutor struct {
	mu        sync.Mutex
	targets   map[uuid.UUID][]uuid.UUID
	submitted []executor.Request
	cancelled []uuid.UUID
	cancelErr error
}

func (f *fakeExecutor) Targets(_ *core.View, r executor.Request) ([]executor.Target, error) {
	ids, ok := f.targets[r.ElementID]
	if !ok {
		return nil, executor.ErrUnsupported
	}
	var out []executor.Target
	for _, id := range ids {
		out = append(out, executor.Target{ElementID: id, Critical: r.Action == vocab.ActionReboot})
	}
	return out, nil
}

func (f *fakeExecutor) Submit(_ context.Context, r executor.Request) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, r)
	return newID(), nil
}

func (f *fakeExecutor) Cancel(_ context.Context, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, ids...)
	return f.cancelErr
}

// fakePresets hält Presets im Speicher; Start liefert startErr.
type fakePresets struct {
	mu        sync.Mutex
	presets   map[uuid.UUID]preset.Preset
	startErr  error
	cancelled []uuid.UUID
}

func (f *fakePresets) List(context.Context) ([]preset.Preset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []preset.Preset
	for _, p := range f.presets {
		out = append(out, preset.Preset{ID: p.ID, Name: p.Name})
	}
	return out, nil
}

func (f *fakePresets) Get(_ context.Context, id uuid.UUID) (preset.Preset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.presets[id]
	if !ok {
		return preset.Preset{}, preset.ErrNotFound
	}
	return p, nil
}

func (f *fakePresets) CreatePreset(_ context.Context, actor uuid.UUID, p preset.Preset) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range p.Steps {
		if p.Steps[i].ID == uuid.Nil {
			p.Steps[i].ID = newID()
		}
	}
	p.CreatedByID = actor
	f.presets[p.ID] = p
	return nil
}

func (f *fakePresets) UpdatePreset(ctx context.Context, actor uuid.UUID, p preset.Preset) error {
	return f.CreatePreset(ctx, actor, p)
}

func (f *fakePresets) DeletePreset(_ context.Context, _, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.presets, id)
	return nil
}

func (f *fakePresets) Start(context.Context, auth.Principal, uuid.UUID) (uuid.UUID, error) {
	return newID(), f.startErr
}

func (f *fakePresets) Cancel(_ context.Context, root uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, root)
	return nil
}

// runWorld ist die Szene mit Executor und Presets als Attrappen. anna sieht und bedient die
// Gruppe G, boss darf alles, viewer sieht alles.
func runWorld(t *testing.T) (*scene, *Server, *fakeExecutor, *fakePresets) {
	t.Helper()
	sc, v := newScene(t)
	s := newTestServer(t)
	s.cfg.Core.(*fakeCore).view.Store(v)
	ex := &fakeExecutor{targets: map[uuid.UUID][]uuid.UUID{sc.p1: {sc.p1}, sc.g: {sc.p1}}}
	pr := &fakePresets{presets: map[uuid.UUID]preset.Preset{}}
	s.cfg.Executor, s.cfg.Presets = ex, pr
	s.cfg.History = &fakeHistory{runs: map[uuid.UUID]action.Run{}}
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{
		anna.UserID: {{Permission: auth.PermView, ScopeElementID: sc.g}, {Permission: auth.PermOperate, ScopeElementID: sc.g}},
		boss.UserID: {
			{Permission: auth.PermView}, {Permission: auth.PermOperate}, {Permission: auth.PermConfigure},
			{Permission: auth.PermCritical}, {Permission: auth.PermPresetRun}, {Permission: auth.PermPresetEdit},
		},
		viewer.UserID: {{Permission: auth.PermView}},
	}
	return sc, s, ex, pr
}

func TestSubmitRun(t *testing.T) {
	sc, s, ex, _ := runWorld(t)
	submit := func(element uuid.UUID, action string) string {
		return `{"element_id":"` + element.String() + `","action":"` + action + `"}`
	}
	run(t, s, []step{
		{"single element", "POST", "/api/v1/runs", submit(sc.p1, "start"), "anna", 202, `"run_id"`},
		{"collective", "POST", "/api/v1/runs", submit(sc.g, "start"), "anna", 202, `"run_id"`},
		{"critical needs critical", "POST", "/api/v1/runs", submit(sc.p1, "reboot"), "anna", 403, `"code":"forbidden"`},
		{"set needs configure", "POST", "/api/v1/runs", submit(sc.p1, "set"), "anna", 403, `"code":"forbidden"`},
		{"invisible target", "POST", "/api/v1/runs", submit(sc.q1, "start"), "anna", 404, ""},
		{"view is not operate", "POST", "/api/v1/runs", submit(sc.p1, "start"), "viewer", 403, ""},
		{"unsupported", "POST", "/api/v1/runs", submit(sc.x9, "start"), "boss", 422, `"code":"unsupported_action"`},
		{"critical with the right", "POST", "/api/v1/runs", submit(sc.p1, "reboot"), "boss", 202, ""},
	})
	if len(ex.submitted) != 3 || ex.submitted[0].Targets != nil || !slices.Equal(ex.submitted[1].Targets, []uuid.UUID{sc.p1}) ||
		ex.submitted[0].Actor != anna.UserID {
		t.Errorf("submitted = %+v; a collective action gets its checked targets, a single one none", ex.submitted)
	}
}

func TestGetAndCancelRun(t *testing.T) {
	sc, s, ex, pr := runWorld(t)
	b := run(t, s, []step{
		{"get", "GET", "/api/v1/runs/" + sc.runP1.String(), "", "anna", 200, `"children":[]`},
		{"preset root needs preset rights", "GET", "/api/v1/runs/" + sc.presetRoot.String(), "", "anna", 404, ""},
		{"not own and no operate", "POST", "/api/v1/runs/" + sc.runP1.String() + "/cancel", "", "viewer", 403, ""},
		{"cancel with operate", "POST", "/api/v1/runs/" + sc.runP1.String() + "/cancel", "", "anna", 204, ""},
		{"cancel own", "POST", "/api/v1/runs/" + sc.runP1.String() + "/cancel", "", "boss", 204, ""},
		{"cancel a preset", "POST", "/api/v1/runs/" + sc.presetRoot.String() + "/cancel", "", "boss", 204, ""},
		{"unknown run", "GET", "/api/v1/runs/" + uuid.NewString(), "", "boss", 404, ""},
	})
	var got struct{ Status string }
	if err := json.Unmarshal(b["get"], &got); err != nil || got.Status != "sent" {
		t.Errorf("run = %s", b["get"])
	}
	if !slices.Equal(ex.cancelled, []uuid.UUID{sc.runP1, sc.runP1}) || !slices.Equal(pr.cancelled, []uuid.UUID{sc.presetRoot}) {
		t.Errorf("cancelled: executor %v, presets %v", ex.cancelled, pr.cancelled)
	}
	ex.cancelErr = executor.ErrFinished
	run(t, s, []step{{"already finished", "POST", "/api/v1/runs/" + sc.runP1.String() + "/cancel", "", "boss", 409, `"code":"run_finished"`}})
}

func TestPresets(t *testing.T) {
	sc, s, _, pr := runWorld(t)
	input := `{"name":"Show A","steps":[{"stage":1,"position":0,"delay_ms":500,"element_id":"` + sc.p1.String() +
		`","action":"start","continue_on_error":false}]}`
	b := run(t, s, []step{
		{"viewer may not list", "GET", "/api/v1/presets", "", "viewer", 403, ""},
		{"create", "POST", "/api/v1/presets", input, "boss", 201, `"delay_ms":500`},
		{"preset_edit needed", "POST", "/api/v1/presets", input, "anna", 403, ""},
		{"list", "GET", "/api/v1/presets", "", "boss", 200, `"name":"Show A"`},
		{"preset_run needed", "POST", "/api/v1/presets/" + uuid.NewString() + "/run", "", "anna", 403, `"missing":[{"permission":"preset_run"}]`},
	})
	id := idOf(t, b["create"])
	if p := pr.presets[uuid.MustParse(id)]; p.CreatedByID != boss.UserID || len(p.Steps) != 1 || p.Steps[0].Delay.Milliseconds() != 500 {
		t.Errorf("stored preset = %+v", p)
	}
	pr.startErr = &preset.MissingRightsError{Missing: []preset.Missing{
		{StepID: uuid.New(), ElementID: sc.p1, Permission: auth.PermOperate},
		{StepID: uuid.New(), ElementID: sc.p1, Permission: auth.PermCritical},
	}}
	b = run(t, s, []step{
		{"missing rights at targets", "POST", "/api/v1/presets/" + id + "/run", "", "boss", 403, `"code":"missing_rights"`},
		{"delete", "DELETE", "/api/v1/presets/" + id, "", "boss", 204, ""},
		{"gone", "GET", "/api/v1/presets/" + id, "", "boss", 404, ""},
	})
	if n := strings.Count(string(b["missing rights at targets"]), `"step_id"`); n != 2 {
		t.Errorf("missing rights = %s", b["missing rights at targets"])
	}
}
