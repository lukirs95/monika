package api

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/history"
)

// fakeHistory merkt sich die Filter und liefert feste Einträge.
type fakeHistory struct {
	mu       sync.Mutex
	findings []evaluation.Finding
	runs     map[uuid.UUID]action.Run
	audit    []history.AuditEntry
	lastF    history.FindingFilter
	lastR    history.RunFilter
	lastA    history.AuditFilter
}

func (f *fakeHistory) ListAudit(_ context.Context, fl history.AuditFilter) ([]history.AuditEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastA = fl
	return f.audit, nil
}

func (f *fakeHistory) ListFindings(_ context.Context, fl history.FindingFilter) ([]evaluation.Finding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastF = fl
	return f.findings[:min(len(f.findings), fl.Limit)], nil
}

func (f *fakeHistory) ListRuns(_ context.Context, fl history.RunFilter) ([]action.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastR = fl
	return nil, nil
}

func (f *fakeHistory) RunByID(_ context.Context, id uuid.UUID) (action.Run, []action.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[id]
	if !ok {
		return action.Run{}, nil, action.ErrNotFound
	}
	var children []action.Run
	for _, c := range f.runs {
		if c.ParentID == id {
			children = append(children, c)
		}
	}
	return r, children, nil
}

func historyWorld(t *testing.T) (*scene, *Server, *fakeHistory) {
	t.Helper()
	sc, s, _, _ := runWorld(t)
	h := s.cfg.History.(*fakeHistory)
	grants := s.cfg.Auth.(*fakeAuth).grants
	grants[boss.UserID] = append(grants[boss.UserID], auth.Grant{Permission: auth.PermAdmin})
	return sc, s, h
}

func TestFindingAndRunHistory(t *testing.T) {
	sc, s, h := historyWorld(t)
	gone := uuid.New() // gelöschtes Element
	for range 3 {
		h.findings = append(h.findings, evaluation.Finding{ID: newID(), ElementID: gone, Origin: evaluation.OriginDevice, Severity: evaluation.SeverityError})
	}
	b := run(t, s, []step{
		{"page", "GET", "/api/v1/findings/history?limit=2&state=withdrawn", "", "boss", 200, `"element_deleted":true`},
		{"invisible element", "GET", "/api/v1/findings/history?element=" + sc.q1.String(), "", "anna", 404, ""},
		{"deleted element needs view global", "GET", "/api/v1/findings/history?element=" + gone.String(), "", "anna", 404, ""},
	})
	var pg struct {
		Items      []struct{ ID uuid.UUID }
		NextBefore *uuid.UUID `json:"next_before"`
	}
	if err := json.Unmarshal(b["page"], &pg); err != nil || len(pg.Items) != 2 || pg.NextBefore == nil || *pg.NextBefore != h.findings[1].ID {
		t.Errorf("page = %s", b["page"])
	}
	if f := h.lastF; f.Limit != 3 || !f.All || f.State != history.FindingWithdrawn || f.Elements != nil {
		t.Errorf("filter = %+v", f)
	}

	tests := []struct {
		name, query, who string
		all, roots       bool
		visible          []uuid.UUID
		elements         []uuid.UUID
	}{
		{"group recursive", "?element=" + sc.g.String() + "&recursive=true", "anna", false, false, sorted(sc.g, sc.p1), sorted(sc.g, sc.p1)},
		{"deleted element", "?element=" + gone.String(), "boss", true, true, nil, []uuid.UUID{gone}},
		{"deleted element recursive", "?element=" + gone.String() + "&recursive=true", "boss", true, true, nil, []uuid.UUID{}},
	}
	for _, tt := range tests {
		run(t, s, []step{{tt.name, "GET", "/api/v1/runs/history" + tt.query, "", tt.who, 200, `"items":[]`}})
		f := h.lastR
		vis, els := slices.Clone(f.Visible), slices.Clone(f.Elements)
		slices.SortFunc(vis, compareIDs)
		slices.SortFunc(els, compareIDs)
		if f.All != tt.all || f.Roots != tt.roots || !slices.Equal(vis, tt.visible) || !slices.Equal(els, tt.elements) || (els == nil) != (tt.elements == nil) {
			t.Errorf("%s: filter = %+v", tt.name, f)
		}
	}
}

func TestStoredRuns(t *testing.T) {
	sc, s, h := historyWorld(t)
	gone, now := uuid.New(), time.Now()
	visible := action.Run{ID: newID(), ElementID: sc.p1, Action: "start", Status: action.StatusConfirmed, FinishedAt: now, RequestedByID: boss.UserID}
	child := action.Run{ID: newID(), ParentID: visible.ID, ElementID: sc.p1, Action: "start", Status: action.StatusConfirmed, FinishedAt: now}
	hidden := action.Run{ID: newID(), ElementID: sc.q1, Action: "start", Status: action.StatusFailed, FinishedAt: now}
	orphan := action.Run{ID: newID(), ElementID: gone, Action: "start", Status: action.StatusFailed, FinishedAt: now}
	for _, r := range []action.Run{visible, child, hidden, orphan} {
		h.runs[r.ID] = r
	}
	run(t, s, []step{
		{"finished run", "GET", "/api/v1/runs/" + visible.ID.String(), "", "anna", 200, `"children":[{`},
		{"invisible target", "GET", "/api/v1/runs/" + hidden.ID.String(), "", "anna", 404, ""},
		{"deleted target with view global", "GET", "/api/v1/runs/" + orphan.ID.String(), "", "boss", 200, `"element_deleted":true`},
		{"deleted target without", "GET", "/api/v1/runs/" + orphan.ID.String(), "", "anna", 404, ""},
		{"cancel a finished run", "POST", "/api/v1/runs/" + visible.ID.String() + "/cancel", "", "anna", 409, `"code":"run_finished"`},
		{"cancel an unknown run", "POST", "/api/v1/runs/" + uuid.NewString() + "/cancel", "", "anna", 404, ""},
	})
	// Ein laufender Lauf zeigt auch seine beendeten Kinder aus dem Verlauf.
	done := newID()
	h.runs[sc.runP1] = action.Run{ID: sc.runP1}
	h.runs[done] = action.Run{ID: done, ParentID: sc.runP1, ElementID: sc.p1, Action: "set", Status: action.StatusConfirmed, FinishedAt: now}
	run(t, s, []step{{"active with finished children", "GET", "/api/v1/runs/" + sc.runP1.String(), "", "anna", 200, done.String()}})
}

func TestAudit(t *testing.T) {
	_, s, h := historyWorld(t)
	h.audit = []history.AuditEntry{{ID: newID(), At: time.Now(), Verb: "create", ObjectType: "element", After: json.RawMessage(`{"label":"X8"}`)}}
	user := uuid.NewString()
	run(t, s, []step{
		{"admin only", "GET", "/api/v1/audit", "", "anna", 403, ""},
		{"monika itself", "GET", "/api/v1/audit?actor=monika&verb=create", "", "boss", 200, `"after":{"label":"X8"}`},
	})
	if f := h.lastA; !f.System || f.Verb != "create" {
		t.Errorf("filter = %+v", f)
	}
	run(t, s, []step{
		{"by user", "GET", "/api/v1/audit?actor=" + user, "", "boss", 200, ""},
		{"bad actor", "GET", "/api/v1/audit?actor=jemand", "", "boss", 400, `"code":"invalid_request"`},
	})
	if f := h.lastA; f.Actor.String() != user || f.System {
		t.Errorf("filter = %+v", f)
	}
}
