package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core/coretest"
	"github.com/lukirs95/monika/internal/forward"
	"github.com/lukirs95/monika/internal/secret"
)

// fakeForwarder hält Ziele im Speicher; Fehler kommen aus err.
type fakeForwarder struct {
	mu      sync.Mutex
	targets map[uuid.UUID]forward.Target
	err     error
}

func (f *fakeForwarder) ListTargets(context.Context) ([]forward.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []forward.Target
	for _, t := range f.targets {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeForwarder) CreateTarget(_ context.Context, _ uuid.UUID, t forward.Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.targets[t.ID] = t
	return nil
}

func (f *fakeForwarder) UpdateTarget(ctx context.Context, actor uuid.UUID, t forward.Target) error {
	f.mu.Lock()
	_, ok := f.targets[t.ID]
	f.mu.Unlock()
	if !ok {
		return forward.ErrNotFound
	}
	return f.CreateTarget(ctx, actor, t)
}

func (f *fakeForwarder) DeleteTarget(_ context.Context, _, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.targets, id)
	return nil
}

// fakeSecrets hält Secrets mit Wert im Speicher; Replace dient dem Core als secret.Service.
type fakeSecrets struct {
	mu     sync.Mutex
	values map[uuid.UUID]json.RawMessage
	meta   map[uuid.UUID]secret.Secret
}

func (f *fakeSecrets) List(context.Context) ([]secret.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []secret.Secret
	for _, s := range f.meta {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeSecrets) Create(_ context.Context, _ uuid.UUID, name, desc string, v json.RawMessage) (secret.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.meta {
		if s.Name == name {
			return secret.Secret{}, secret.ErrNameTaken
		}
	}
	s := secret.Secret{ID: newID(), Name: name, Description: desc, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.meta[s.ID], f.values[s.ID] = s, v
	return s, nil
}

func (f *fakeSecrets) Delete(_ context.Context, _, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.meta[id]; !ok {
		return secret.ErrNotFound
	}
	delete(f.meta, id)
	return nil
}

func (f *fakeSecrets) Replace(_ context.Context, _, id uuid.UUID, v json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.meta[id]; !ok {
		return secret.ErrNotFound
	}
	f.values[id] = v
	return nil
}

func TestForwardTargets(t *testing.T) {
	s := newTestServer(t)
	fw := &fakeForwarder{targets: map[uuid.UUID]forward.Target{}}
	s.cfg.Forwarder = fw
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{boss.UserID: {{Permission: auth.PermAdmin}}, viewer.UserID: {{Permission: auth.PermView}}}
	input := `{"name":"Leitstelle","kind":"webhook","enabled":true,"config":{"url":"https://hooks.example/m"},` +
		`"events":["finding.raised"],"min_severity":"warning"}`
	b := run(t, s, []step{
		{"admin only", "GET", "/api/v1/forward-targets", "", "viewer", 403, ""},
		{"create", "POST", "/api/v1/forward-targets", input, "boss", 201, `"config":{"url":"https://hooks.example/m"}`},
		{"unknown event", "POST", "/api/v1/forward-targets", strings.Replace(input, "finding.raised", "finding.exploded", 1), "boss", 400, `"code":"invalid_request"`},
		{"update unknown", "PUT", "/api/v1/forward-targets/" + uuid.NewString(), input, "boss", 404, ""},
	})
	id := idOf(t, b["create"])
	fw.err = forward.ErrSecretInBody
	run(t, s, []step{
		{"secret in body", "POST", "/api/v1/forward-targets", input, "boss", 422, `"code":"secret_in_body"`},
		{"update", "PUT", "/api/v1/forward-targets/" + id, strings.Replace(input, "warning", "error", 1), "boss", 422, ""},
	})
	fw.err = nil
	run(t, s, []step{
		{"update", "PUT", "/api/v1/forward-targets/" + id, strings.Replace(input, "warning", "error", 1), "boss", 200, `"min_severity":"error"`},
		{"list", "GET", "/api/v1/forward-targets", "", "boss", 200, `"name":"Leitstelle"`},
		{"delete", "DELETE", "/api/v1/forward-targets/" + id, "", "boss", 204, ""},
	})
}

func TestSecrets(t *testing.T) {
	sc, _ := newScene(t)
	secrets := &fakeSecrets{values: map[uuid.UUID]json.RawMessage{}, meta: map[uuid.UUID]secret.Secret{}}
	s := newServerWith(t, coretest.Start(t, coretest.Options{Snapshot: sc.snapshot(), Secrets: secrets}))
	s.cfg.Secrets = secrets
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{
		boss.UserID: {{Permission: auth.PermAdmin}}, anna.UserID: {{Permission: auth.PermSourceEdit}}, viewer.UserID: {{Permission: auth.PermView}},
	}
	value := `{"username":"admin","password":"geheim-1234"}`
	b := run(t, s, []step{
		{"create", "POST", "/api/v1/secrets", `{"name":"xlink","value":` + value + `}`, "boss", 201, `"name":"xlink"`},
		{"name taken", "POST", "/api/v1/secrets", `{"name":"xlink","value":{}}`, "boss", 409, `"code":"name_taken"`},
		{"source_edit may list", "GET", "/api/v1/secrets", "", "anna", 200, `"name":"xlink"`},
		{"but not create", "POST", "/api/v1/secrets", `{"name":"x","value":{}}`, "anna", 403, ""},
		{"viewer may not list", "GET", "/api/v1/secrets", "", "viewer", 403, ""},
	})
	for name, body := range b {
		if strings.Contains(string(body), "geheim") {
			t.Errorf("%s returned the value: %s", name, body)
		}
	}
	id := idOf(t, b["create"])
	run(t, s, []step{
		{"replace", "PUT", "/api/v1/secrets/" + id + "/value", `{"value":{"password":"neu-5678"}}`, "boss", 204, ""},
		{"replace unknown", "PUT", "/api/v1/secrets/" + uuid.NewString() + "/value", `{"value":{}}`, "boss", 404, ""},
		{"delete", "DELETE", "/api/v1/secrets/" + id, "", "boss", 204, ""},
		{"deleted", "DELETE", "/api/v1/secrets/" + id, "", "boss", 404, ""},
	})
	if v := string(secrets.values[uuid.MustParse(id)]); v != `{"password":"neu-5678"}` {
		t.Errorf("value after replace = %s", v)
	}
}
