package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/sdk"
)

var (
	anna  = auth.Principal{UserID: uuid.New(), Username: "anna", SessionID: uuid.New()}
	wall  = auth.Principal{UserID: uuid.New(), Username: "wand", Service: true, SessionID: uuid.New(), ReadOnly: true}
	robot = auth.Principal{UserID: uuid.New(), Username: "companion", Service: true, TokenID: uuid.New()}
	boss  = auth.Principal{UserID: uuid.New(), Username: "chefin", SessionID: uuid.New()}
)

// fakeAuth kennt zwei Sitzungen und ein Token; die übrigen Methoden stehen in auth_test.go.
type fakeAuth struct {
	Auth
	mu        sync.Mutex
	meta      auth.Meta // der letzten Anmeldung
	loggedOut []string
	grants    map[uuid.UUID][]auth.Grant // pro Benutzer; sonst die aus auth_test.go
}

func (*fakeAuth) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	switch token {
	case "anna":
		return anna, nil
	case "kiosk":
		return wall, nil
	case "boss":
		return boss, nil
	}
	return auth.Principal{}, auth.ErrUnauthenticated
}

func (*fakeAuth) AuthenticateToken(_ context.Context, bearer string) (auth.Principal, error) {
	if bearer == "monika_ok" {
		return robot, nil
	}
	return auth.Principal{}, auth.ErrUnauthenticated
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	public, _ := url.Parse("https://monika.example")
	s, err := New(Config{
		Auth: &fakeAuth{}, Core: &fakeCore{}, PublicURL: public, Log: slog.New(slog.DiscardHandler),
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")},
		Drivers:        func() []sdk.Driver { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestGuard(t *testing.T) {
	s := newTestServer(t)
	var got auth.Principal
	h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = principal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	const origin = "https://monika.example"
	stream := "/api/v1/events/" + uuid.NewString() + "/watch"

	tests := []struct {
		name, method, path, body string
		cookie, bearer, origin   string
		status                   int
		code                     string
		want                     auth.Principal
	}{
		{name: "no credentials", method: "GET", path: "/api/v1/state", status: 401, code: "unauthenticated"},
		{name: "session", method: "GET", path: "/api/v1/state", cookie: "anna", status: 204, want: anna},
		{name: "unknown session", method: "GET", path: "/api/v1/state", cookie: "nobody", status: 401, code: "unauthenticated"},
		{name: "token", method: "GET", path: "/api/v1/state", bearer: "Bearer monika_ok", status: 204, want: robot},
		{name: "bad token does not fall back to the cookie", method: "GET", path: "/api/v1/state", cookie: "anna", bearer: "Bearer monika_bad", status: 401, code: "unauthenticated"},
		{name: "not a bearer", method: "GET", path: "/api/v1/state", bearer: "Basic YTpi", status: 401, code: "unauthenticated"},
		{name: "public operation", method: "POST", path: "/api/v1/auth/login", body: `{"username":"anna","password":"x"}`, status: 204},
		{name: "public with foreign origin", method: "POST", path: "/api/v1/auth/login", body: `{"username":"anna","password":"x"}`, origin: "https://evil.example", status: 403, code: "origin_mismatch"},
		{name: "session change without origin", method: "POST", path: "/api/v1/devices", body: `{"label":"X8"}`, cookie: "anna", status: 403, code: "origin_mismatch"},
		{name: "session change with foreign origin", method: "POST", path: "/api/v1/devices", body: `{"label":"X8"}`, cookie: "anna", origin: "https://evil.example", status: 403, code: "origin_mismatch"},
		{name: "session change", method: "POST", path: "/api/v1/devices", body: `{"label":"X8"}`, cookie: "anna", origin: origin, status: 204, want: anna},
		{name: "token change without origin", method: "POST", path: "/api/v1/devices", body: `{"label":"X8"}`, bearer: "Bearer monika_ok", status: 204, want: robot},
		{name: "read only", method: "POST", path: "/api/v1/devices", body: `{"label":"X8"}`, cookie: "kiosk", origin: origin, status: 403, code: "read_only"},
		{name: "read only may watch", method: "POST", path: stream, body: `{}`, cookie: "kiosk", origin: origin, status: 204, want: wall},
		{name: "read only may log out", method: "POST", path: "/api/v1/auth/logout", cookie: "kiosk", origin: origin, status: 204, want: wall},
		{name: "logout only with a session", method: "POST", path: "/api/v1/auth/logout", bearer: "Bearer monika_ok", status: 401, code: "unauthenticated"},
		{name: "unknown path", method: "GET", path: "/api/v1/nothing", cookie: "anna", status: 404, code: "not_found"},
		{name: "wrong method", method: "DELETE", path: "/api/v1/state", cookie: "anna", origin: origin, status: 405, code: "method_not_allowed"},
		{name: "too short", method: "POST", path: "/api/v1/devices", body: `{"label":""}`, cookie: "anna", origin: origin, status: 400, code: "invalid_request"},
		{name: "unknown enum", method: "GET", path: "/api/v1/findings/history?severity=fatal", cookie: "anna", status: 400, code: "invalid_request"},
		{name: "limit too high", method: "GET", path: "/api/v1/runs/history?limit=501", cookie: "anna", status: 400, code: "invalid_request"},
		{name: "authentication before validation", method: "POST", path: "/api/v1/devices", body: `{"label":""}`, status: 401, code: "unauthenticated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got = auth.Principal{}
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			r := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, body)
			if tt.body != "" {
				r.Header.Set("Content-Type", "application/json")
			}
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: SessionCookie, Value: tt.cookie})
			}
			if tt.bearer != "" {
				r.Header.Set("Authorization", tt.bearer)
			}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.status, w.Body)
			}
			if tt.code != "" {
				var p Problem
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Code != tt.code {
					t.Errorf("problem = %s, want code %s", w.Body, tt.code)
				}
				if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
					t.Errorf("content type = %q", ct)
				}
			}
			if got != tt.want {
				t.Errorf("principal = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Formate wie uuid prüft der erzeugte Code hinter dem Guard.
func TestHandlerRejectsBadUUID(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/elements/x8/facts", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "anna"})
	w := httptest.NewRecorder()
	newTestServer(t).Handler().ServeHTTP(w, r)
	var p Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); w.Code != http.StatusBadRequest || err != nil || p.Code != "invalid_request" {
		t.Errorf("response = %d %s", w.Code, w.Body)
	}
}

// Die Beschreibung eines Prüffehlers nennt Ort und Grund, nie den Wert.
func TestValidationHidesValues(t *testing.T) {
	s := newTestServer(t)
	h := s.guard(http.NotFoundHandler())
	r := httptest.NewRequestWithContext(t.Context(), "PUT", "/api/v1/users/"+uuid.NewString()+"/password", strings.NewReader(`{"password":"geheim-kurz"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer monika_ok")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "geheim") || !strings.Contains(w.Body.String(), "password") {
		t.Errorf("response = %d %s", w.Code, w.Body)
	}
}

// Ohne Anmeldung erreichbar sind nur die Schritte der Anmeldung und /healthz (api.md). Jede andere
// Operation der Spezifikation antwortet ohne Anmeldung mit 401, bevor ein Handler läuft.
func TestOnlyLoginIsPublic(t *testing.T) {
	wantPublic := map[string]bool{
		"Login": true, "LoginTOTP": true, "EnrollTOTP": true, "ConfirmTOTP": true, "KioskLogin": true,
		"GetHealthz": true,
	}
	s := newTestServer(t)
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	var reached bool
	h := s.guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	params := regexp.MustCompile(`\{[a-z_]+\}`)
	ops := 0
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			ops++
			if public(op) != wantPublic[op.OperationID] {
				t.Errorf("%s %s (%s): public = %v", method, path, op.OperationID, public(op))
			}
			if wantPublic[op.OperationID] {
				continue
			}
			reached = false
			target := Prefix + params.ReplaceAllStringFunc(path, func(string) string { return uuid.NewString() })
			r := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized || reached {
				t.Errorf("%s %s without credentials = %d, handler reached %v", method, target, w.Code, reached)
			}
		}
	}
	if ops != 68 {
		t.Errorf("operations = %d, want 68", ops)
	}
}
