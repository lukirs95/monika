//go:build integration

package postgres_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/history"
	"github.com/lukirs95/monika/internal/preset"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func findingIDs(fs []evaluation.Finding) []uuid.UUID {
	var out []uuid.UUID
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

func runIDs(rs []action.Run) []uuid.UUID {
	var out []uuid.UUID
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func TestFindingHistory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	a, b := newID(), newID() // Elemente; ohne Fremdschlüssel, b gilt als gelöscht
	raise := func(element uuid.UUID, severity evaluation.Severity) uuid.UUID {
		id := newID()
		if err := s.RaiseFinding(ctx, evaluation.Finding{ID: id, ElementID: element, ElementPath: "X8", Origin: evaluation.OriginDevice,
			Code: "los-" + id.String(), Severity: severity, RaisedAt: time.Now()}); err != nil {
			t.Fatalf("RaiseFinding: %v", err)
		}
		return id
	}
	active, cleared, withdrawn, other := raise(a, evaluation.SeverityError), raise(a, evaluation.SeverityWarning), raise(a, evaluation.SeverityError), raise(b, evaluation.SeverityError)
	if err := s.EndFinding(ctx, cleared, evaluation.ReasonResolved); err != nil {
		t.Fatalf("EndFinding: %v", err)
	}
	if err := s.EndFinding(ctx, withdrawn, evaluation.ReasonDeleted); err != nil {
		t.Fatalf("EndFinding: %v", err)
	}
	all := history.Sight{All: true}
	tests := []struct {
		name string
		f    history.FindingFilter
		want []uuid.UUID
	}{
		{"everything, newest first", history.FindingFilter{Sight: all}, []uuid.UUID{other, withdrawn, cleared, active}},
		{"visible only", history.FindingFilter{Sight: history.Sight{Visible: []uuid.UUID{a}}}, []uuid.UUID{withdrawn, cleared, active}},
		{"nothing visible", history.FindingFilter{Sight: history.Sight{Visible: []uuid.UUID{}}}, nil},
		{"element filter", history.FindingFilter{Sight: history.Sight{All: true, Elements: []uuid.UUID{b}}}, []uuid.UUID{other}},
		{"empty element filter", history.FindingFilter{Sight: history.Sight{All: true, Elements: []uuid.UUID{}}}, nil},
		{"active", history.FindingFilter{Sight: all, State: history.FindingActive}, []uuid.UUID{other, active}},
		{"cleared", history.FindingFilter{Sight: all, State: history.FindingCleared}, []uuid.UUID{cleared}},
		{"withdrawn", history.FindingFilter{Sight: all, State: history.FindingWithdrawn}, []uuid.UUID{withdrawn}},
		{"severity", history.FindingFilter{Sight: all, Severity: evaluation.SeverityWarning}, []uuid.UUID{cleared}},
		{"page", history.FindingFilter{Sight: all, Page: history.Page{Before: withdrawn, Limit: 1}}, []uuid.UUID{cleared}},
		{"time range", history.FindingFilter{Sight: all, Page: history.Page{To: time.Now().Add(-time.Hour)}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.f.Limit == 0 {
				tt.f.Limit = 100
			}
			got, err := s.ListFindings(ctx, tt.f)
			if err != nil || !slices.Equal(findingIDs(got), tt.want) {
				t.Errorf("ListFindings = %v, %v; want %v", findingIDs(got), err, tt.want)
			}
		})
	}
}

func TestRunHistory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)
	_, port, group := presetFixture(t, s)
	p := preset.Preset{ID: newID(), Name: "Show", Steps: []preset.Step{step(1, group, "start")}}
	if err := s.CreatePreset(ctx, user, p); err != nil {
		t.Fatalf("CreatePreset: %v", err)
	}
	root := action.Run{ID: newID(), PresetID: p.ID, Action: "preset", Status: action.StatusPending, RequestedByID: user}
	parent := action.Run{ID: newID(), ParentID: root.ID, ElementID: group, Action: "start", Status: action.StatusPending, RequestedByID: user}
	child := action.Run{ID: newID(), ParentID: parent.ID, ElementID: port, Action: "start", Status: action.StatusConfirmed, RequestedByID: user,
		Verify:     &action.Verify{Timeout: 10 * time.Second, Conditions: []evaluation.Condition{{Fact: "state.running", Op: evaluation.OpEq, Value: json.RawMessage(`true`)}}},
		FinishedAt: time.Now()}
	if err := s.CreateRuns(ctx, []action.Run{root, parent, child}); err != nil {
		t.Fatalf("CreateRuns: %v", err)
	}
	if err := s.UpdateRun(ctx, child); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	tests := []struct {
		name string
		f    history.RunFilter
		want []uuid.UUID
	}{
		{"with preset roots", history.RunFilter{Sight: history.Sight{All: true}, Roots: true}, []uuid.UUID{child.ID, parent.ID, root.ID}},
		{"without preset rights", history.RunFilter{Sight: history.Sight{All: true}}, []uuid.UUID{child.ID, parent.ID}},
		{"visible port only", history.RunFilter{Sight: history.Sight{Visible: []uuid.UUID{port}}, Roots: true}, []uuid.UUID{child.ID, root.ID}},
		{"status", history.RunFilter{Sight: history.Sight{All: true}, Status: action.StatusConfirmed}, []uuid.UUID{child.ID}},
		{"requested by someone else", history.RunFilter{Sight: history.Sight{All: true}, Roots: true, RequestedBy: newID()}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.f.Limit = 100
			got, err := s.ListRuns(ctx, tt.f)
			want := tt.want
			if want == nil {
				want = []uuid.UUID{}
			}
			ids := runIDs(got)
			if ids == nil {
				ids = []uuid.UUID{}
			}
			if err != nil || !slices.Equal(ids, want) {
				t.Errorf("ListRuns = %v, %v; want %v", ids, err, want)
			}
		})
	}

	got, children, err := s.RunByID(ctx, parent.ID)
	if err != nil || got.ElementID != group || len(children) != 1 || children[0].ID != child.ID ||
		children[0].Verify == nil || children[0].Verify.Timeout != 10*time.Second || children[0].FinishedAt.IsZero() {
		t.Fatalf("RunByID = %+v, %+v, %v", got, children, err)
	}
	if _, _, err := s.RunByID(ctx, newID()); !errors.Is(err, action.ErrNotFound) {
		t.Errorf("RunByID unknown = %v", err)
	}
}

func TestAuditHistory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	admin := newUser(t, pool)
	device := newID()
	if err := s.CreateDevice(ctx, admin, deviceElement(device)); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := s.CreateDevice(ctx, uuid.Nil, deviceElement(newID())); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	all, err := s.ListAudit(ctx, history.AuditFilter{Page: history.Page{Limit: 100}})
	if err != nil || len(all) < 2 {
		t.Fatalf("ListAudit = %d, %v", len(all), err)
	}
	byAdmin, _ := s.ListAudit(ctx, history.AuditFilter{Page: history.Page{Limit: 100}, Actor: admin, ObjectID: device})
	system, _ := s.ListAudit(ctx, history.AuditFilter{Page: history.Page{Limit: 100}, System: true, ObjectType: "element"})
	if len(byAdmin) != 1 || byAdmin[0].ActorID != admin || len(system) != 1 || system[0].ActorID != uuid.Nil {
		t.Errorf("by admin = %+v, system = %+v", byAdmin, system)
	}
}

func deviceElement(id uuid.UUID) inventory.Element {
	return inventory.Element{ID: id, Kind: inventory.KindDevice, DeviceID: id, Label: "X8"}
}
