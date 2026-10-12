//go:build integration

package postgres_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/forward"
	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestForwardTargets(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)

	device := newID()
	if err := s.CreateDevice(ctx, uuid.Nil, inventory.Element{ID: device}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	target := forward.Target{
		ID: newID(), Name: "Leitstelle", Kind: forward.KindWebhook, Enabled: true,
		Config:         json.RawMessage(`{"url": "https://hooks.example/monika", "method": "POST"}`),
		Events:         []string{forward.EventFindingRaised, forward.EventFindingCleared, forward.EventFindingWithdrawn},
		MinSeverity:    evaluation.SeverityWarning,
		ScopeElementID: device,
	}
	if err := s.CreateTarget(ctx, user, target); err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	list, err := s.ListTargets(ctx)
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], target) {
		t.Fatalf("ListTargets = %+v, %v;\nwant %+v", list, err, target)
	}
	unknown := target
	unknown.ID, unknown.SecretID = newID(), newID()
	if err := s.CreateTarget(ctx, user, unknown); !errors.Is(err, forward.ErrSecretNotFound) {
		t.Errorf("CreateTarget with an unknown secret = %v", err)
	}
	unknown.ID = target.ID
	if err := s.UpdateTarget(ctx, user, unknown); !errors.Is(err, forward.ErrSecretNotFound) {
		t.Errorf("UpdateTarget with an unknown secret = %v", err)
	}

	t.Run("results", func(t *testing.T) {
		ok := time.Now().Truncate(time.Microsecond)
		steps := []struct {
			result        forward.Result
			wantSent      time.Time
			wantLastError string
		}{
			{forward.Result{TargetID: target.ID, At: ok}, ok, ""},
			{forward.Result{TargetID: target.ID, At: ok.Add(time.Minute), Error: "503 Service Unavailable"}, ok, "503 Service Unavailable"},
			{forward.Result{TargetID: target.ID, At: ok.Add(2 * time.Minute)}, ok.Add(2 * time.Minute), ""},
		}
		for i, st := range steps {
			if err := s.SetTargetResults(ctx, []forward.Result{st.result}); err != nil {
				t.Fatalf("step %d: SetTargetResults: %v", i, err)
			}
			list, err := s.ListTargets(ctx)
			if err != nil || !list[0].LastSentAt.Equal(st.wantSent) || list[0].LastError != st.wantLastError {
				t.Fatalf("step %d: last_sent_at %v, last_error %q, err %v; want %v, %q", i, list[0].LastSentAt, list[0].LastError, err, st.wantSent, st.wantLastError)
			}
		}
	})

	t.Run("update and delete", func(t *testing.T) {
		changed := target
		changed.Enabled, changed.MinSeverity = false, ""
		if err := s.UpdateTarget(ctx, user, changed); err != nil {
			t.Fatalf("UpdateTarget: %v", err)
		}
		if err := s.DeleteTarget(ctx, user, target.ID); err != nil {
			t.Fatalf("DeleteTarget: %v", err)
		}
		for name, err := range map[string]error{
			"update unknown": s.UpdateTarget(ctx, user, changed),
			"delete unknown": s.DeleteTarget(ctx, user, target.ID),
		} {
			if !errors.Is(err, forward.ErrNotFound) {
				t.Errorf("%s = %v, want ErrNotFound", name, err)
			}
		}
	})

	var verbs []string
	for _, e := range auditLog(t, pool) {
		if e.ObjectType == "forward_target" {
			verbs = append(verbs, e.Verb)
		}
	}
	if !slices.Equal(verbs, []string{"create", "update", "delete"}) {
		t.Errorf("audit = %v, want create, update, delete", verbs)
	}
}
