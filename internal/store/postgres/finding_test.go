//go:build integration

package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestFindings(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)

	device := newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	raisedAt := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	cause := evaluation.Finding{
		ID: newID(), ElementID: device, Origin: evaluation.OriginSystem, Code: "source.unreachable",
		Severity: evaluation.SeverityError, Message: "XLink-API nicht erreichbar",
		Details: json.RawMessage(`{"reason": "connection refused"}`), RaisedAt: raisedAt,
	}
	symptom := evaluation.Finding{
		ID: newID(), ElementID: device, Origin: evaluation.OriginDevice, Code: "srt.error",
		Severity: evaluation.SeverityWarning,
	}
	for _, f := range []evaluation.Finding{cause, symptom} {
		if err := s.RaiseFinding(ctx, f); err != nil {
			t.Fatalf("RaiseFinding %s: %v", f.Code, err)
		}
	}

	t.Run("same finding cannot be active twice", func(t *testing.T) {
		again := cause
		again.ID = newID()
		if err := s.RaiseFinding(ctx, again); !errors.Is(err, evaluation.ErrAlreadyActive) {
			t.Fatalf("RaiseFinding = %v, want ErrAlreadyActive", err)
		}
	})

	t.Run("cause, ack and list", func(t *testing.T) {
		if err := s.SetFindingCause(ctx, symptom.ID, cause.ID); err != nil {
			t.Fatalf("SetFindingCause: %v", err)
		}
		if err := s.AckFinding(ctx, user, cause.ID); err != nil {
			t.Fatalf("AckFinding: %v", err)
		}
		active, err := s.ListActiveFindings(ctx)
		if err != nil || len(active) != 2 {
			t.Fatalf("ListActiveFindings = %+v, %v; want 2", active, err)
		}
		got := map[uuid.UUID]evaluation.Finding{}
		for _, f := range active {
			got[f.ID] = f
		}
		if c := got[cause.ID]; !c.RaisedAt.Equal(raisedAt) || c.AckedByID != user || c.AckedAt.IsZero() || c.Message != cause.Message {
			t.Errorf("cause = %+v, want given raised_at, acked by %s and message", c, user)
		}
		if sym := got[symptom.ID]; sym.CausedByID != cause.ID || sym.RaisedAt.IsZero() {
			t.Errorf("symptom = %+v, want caused by %s and raised_at set", sym, cause.ID)
		}
	})

	t.Run("end and raise again", func(t *testing.T) {
		if err := s.EndFinding(ctx, cause.ID, evaluation.ReasonResolved); err != nil {
			t.Fatalf("EndFinding: %v", err)
		}
		if cleared, reason, _ := findingEnd(t, pool, cause.ID); !cleared || reason != "resolved" {
			t.Fatalf("finding cleared = %v, reason %q; want resolved", cleared, reason)
		}
		for name, err := range map[string]error{
			"end again":   s.EndFinding(ctx, cause.ID, evaluation.ReasonResolved),
			"ack ended":   s.AckFinding(ctx, user, cause.ID),
			"cause ended": s.SetFindingCause(ctx, cause.ID, uuid.Nil),
		} {
			if !errors.Is(err, evaluation.ErrNotFound) {
				t.Errorf("%s = %v, want ErrNotFound", name, err)
			}
		}
		// Nach dem Ende darf derselbe Befund erneut entstehen.
		again := cause
		again.ID = newID()
		if err := s.RaiseFinding(ctx, again); err != nil {
			t.Fatalf("RaiseFinding after end: %v", err)
		}
	})
}
