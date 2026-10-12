package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/inventory"
)

func TestToProblem(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
		detail string
	}{
		{"wrapped domain error", fmt.Errorf("create source: %w", inventory.ErrSourceExists), 409, "source_exists", "create source: device already has a source with this driver"},
		{"not found", fmt.Errorf("delete device: %w", inventory.ErrNotFound), 404, "not_found", ""},
		{"run finished", executor.ErrFinished, 409, "run_finished", ""},
		{"problem of a handler", errReadOnly, 403, "read_only", "read-only access"},
		{"core stopped", core.ErrStopped, 503, "unavailable", ""},
		{"cancelled", fmt.Errorf("load: %w", context.Canceled), 503, "unavailable", ""},
		{"unknown error has no details", errors.New("pq: password=geheim"), 500, "internal", "internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := toProblem(tt.err)
			if p.Status != tt.status || p.Code != tt.code || (tt.detail != "" && p.Detail != tt.detail) {
				t.Errorf("toProblem = %d %s %q, want %d %s %q", p.Status, p.Code, p.Detail, tt.status, tt.code, tt.detail)
			}
		})
	}
}

func TestWriteProblem(t *testing.T) {
	w := httptest.NewRecorder()
	writeProblem(w, toProblem(fmt.Errorf("login: %w", auth.ThrottledError{Wait: 1500 * time.Millisecond})))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "2" {
		t.Errorf("throttled = %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}

	preset := uuid.New()
	w = httptest.NewRecorder()
	err := fmt.Errorf("delete device: %w", &inventory.InUseError{Presets: []inventory.PresetRef{{ID: preset, Name: "Show A"}}})
	writeProblem(w, toProblem(inUse(err, "device_in_use", true)))
	var got struct {
		Type, Code string
		Status     int
		Presets    []struct{ ID, Name string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %s: %v", w.Body, err)
	}
	if got.Type != "about:blank" || got.Status != 409 || got.Code != "device_in_use" || len(got.Presets) != 1 ||
		got.Presets[0].ID != preset.String() || got.Presets[0].Name != "Show A" {
		t.Errorf("in use = %s", w.Body)
	}
	hidden := toProblem(inUse(err, "device_in_use", false))
	if b, _ := json.Marshal(hidden); !strings.Contains(string(b), `"presets":[]`) || strings.Contains(string(b), "Show A") {
		t.Errorf("hidden presets = %s", b)
	}
	if err := inUse(inventory.ErrNotFound, "device_in_use", true); !errors.Is(err, inventory.ErrNotFound) || toProblem(err).Code != "not_found" {
		t.Error("inUse changed another error")
	}
}
