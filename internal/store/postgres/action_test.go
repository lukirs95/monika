//go:build integration

package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/preset"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func runStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (status string, finished bool, reason string) {
	t.Helper()
	var r *string
	if err := pool.QueryRow(t.Context(),
		`SELECT status, finished_at IS NOT NULL, response->>'reason' FROM action_run WHERE id = $1`, id,
	).Scan(&status, &finished, &r); err != nil {
		t.Fatalf("query run: %v", err)
	}
	return status, finished, value(r)
}

func TestRuns(t *testing.T) {
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
	stepRun := action.Run{ID: newID(), ParentID: root.ID, PresetStepID: p.Steps[0].ID, ElementID: group,
		Action: "start", Status: action.StatusPending, RequestedByID: user}
	elementRun := action.Run{ID: newID(), ParentID: stepRun.ID, ElementID: port, Action: "start",
		Status: action.StatusPending, RequestedByID: user, Critical: true,
		Verify: &action.Verify{Timeout: 10 * time.Second, Conditions: []evaluation.Condition{
			{Fact: "running", Op: evaluation.OpEq, Value: json.RawMessage(`true`)},
		}}}
	// Kinder vor Eltern: Die Fremdschlüssel werden erst am Ende der Transaktion geprüft.
	if err := s.CreateRuns(ctx, []action.Run{elementRun, stepRun, root}); err != nil {
		t.Fatalf("CreateRuns: %v", err)
	}

	var verify string
	if err := pool.QueryRow(ctx, `SELECT verify::text FROM action_run WHERE id = $1`, elementRun.ID).Scan(&verify); err != nil {
		t.Fatalf("query verify: %v", err)
	}
	if want := `{"conditions": [{"op": "eq", "fact": "running", "value": true}], "timeout_ms": 10000}`; verify != want {
		t.Errorf("verify = %s, want %s", verify, want)
	}

	t.Run("update", func(t *testing.T) {
		sent := elementRun
		sent.Status, sent.SentAt = action.StatusSent, time.Now()
		if err := s.UpdateRun(ctx, sent); err != nil {
			t.Fatalf("UpdateRun: %v", err)
		}
		if status, _, _ := runStatus(t, pool, elementRun.ID); status != "sent" {
			t.Fatalf("status = %s, want sent", status)
		}
		if err := s.UpdateRun(ctx, action.Run{ID: newID(), Status: action.StatusFailed}); !errors.Is(err, action.ErrNotFound) {
			t.Fatalf("UpdateRun unknown = %v, want ErrNotFound", err)
		}
	})

	t.Run("finish interrupted runs", func(t *testing.T) {
		accepted := action.Run{ID: newID(), ElementID: port, Action: "stop", Status: action.StatusAccepted, RequestedByID: user}
		done := action.Run{ID: newID(), ElementID: port, Action: "stop", Status: action.StatusConfirmed, RequestedByID: user}
		if err := s.CreateRuns(ctx, []action.Run{accepted, done}); err != nil {
			t.Fatalf("CreateRuns: %v", err)
		}
		done.FinishedAt = time.Now()
		if err := s.UpdateRun(ctx, done); err != nil {
			t.Fatalf("UpdateRun: %v", err)
		}

		n, err := s.FinishInterruptedRuns(ctx, "Monika neu gestartet")
		if err != nil || n != 4 { // root, stepRun (pending), elementRun (sent), accepted
			t.Fatalf("FinishInterruptedRuns = %d, %v; want 4", n, err)
		}
		want := map[uuid.UUID]string{root.ID: "failed", elementRun.ID: "failed", accepted.ID: "unconfirmed", done.ID: "confirmed"}
		for id, status := range want {
			got, finished, reason := runStatus(t, pool, id)
			wantReason := "Monika neu gestartet"
			if id == done.ID {
				wantReason = ""
			}
			if got != status || !finished || reason != wantReason {
				t.Errorf("run %s = %s, finished %v, reason %q; want %s, finished, reason %q", id, got, finished, reason, status, wantReason)
			}
		}
	})
}
