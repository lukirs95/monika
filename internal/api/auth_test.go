package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
)

var expires = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func (f *fakeAuth) Login(_ context.Context, username, password string, meta auth.Meta) (auth.LoginStep, error) {
	f.mu.Lock()
	f.meta = meta
	f.mu.Unlock()
	switch {
	case username == "locked":
		return auth.LoginStep{}, auth.ThrottledError{Wait: 30 * time.Second}
	case password != "correct horse battery":
		return auth.LoginStep{}, auth.ErrInvalidCredentials
	}
	return auth.LoginStep{Challenge: "challenge", NeedsEnrollment: username == "new"}, nil
}

// CheckSession: Sitzungen gelten, bis ended sie enthält.
func (f *fakeAuth) CheckSession(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if slices.Contains(f.ended, token) {
		return auth.ErrUnauthenticated
	}
	return nil
}

func (*fakeAuth) CheckToken(context.Context, string) error { return nil }
func (*fakeAuth) AuthzVersion() uint64                     { return 0 }

func (*fakeAuth) VerifyTOTP(_ context.Context, challenge, code string, _ auth.Meta) (auth.IssuedSession, error) {
	if challenge != "challenge" || code != "123456" {
		return auth.IssuedSession{}, auth.ErrInvalidCode
	}
	return auth.IssuedSession{Token: "fresh", ExpiresAt: expires}, nil
}

func (*fakeAuth) EnrollTOTP(context.Context, string) (auth.Enrollment, error) {
	return auth.Enrollment{OTPAuthURL: "otpauth://totp/Monika:anna", Secret: "ABC"}, nil
}

func (*fakeAuth) ConfirmTOTP(context.Context, string, string, auth.Meta) (auth.IssuedSession, []string, error) {
	return auth.IssuedSession{Token: "fresh", ExpiresAt: expires}, []string{"aaaaa-bbbbb"}, nil
}

func (*fakeAuth) KioskLogin(_ context.Context, bearer string, _ auth.Meta) (auth.IssuedSession, error) {
	if bearer != "monika_wall" {
		return auth.IssuedSession{}, auth.ErrUnauthenticated
	}
	return auth.IssuedSession{Token: "kiosk", ExpiresAt: expires}, nil
}

func (f *fakeAuth) Logout(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loggedOut = append(f.loggedOut, token)
	return nil
}

func (*fakeAuth) ChangePassword(_ context.Context, _ auth.Principal, current, _ string, _ auth.Meta) error {
	if current != "correct horse battery" {
		return auth.ErrInvalidCredentials
	}
	return nil
}

func (*fakeAuth) ListSessions(_ context.Context, user uuid.UUID) ([]auth.Session, error) {
	return []auth.Session{
		{ID: anna.SessionID, UserID: user, Kind: auth.SessionUser, UserAgent: "Firefox", ExpiresAt: expires},
		{ID: uuid.New(), UserID: user, Kind: auth.SessionUser, ExpiresAt: expires},
	}, nil
}

func (*fakeAuth) RevokeSession(_ context.Context, _, id uuid.UUID) error {
	if id != anna.SessionID {
		return auth.ErrNotFound
	}
	return nil
}

func (f *fakeAuth) Grants(user uuid.UUID) []auth.Grant {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.grants[user]; ok {
		return g
	}
	return []auth.Grant{{Permission: auth.PermView}, {Permission: auth.PermOperate, ScopeElementID: scope}}
}

var scope = uuid.New()

// Allowed wie auth.Service: Ein Recht gilt global oder für Elemente in seinem Geltungsbereich.
func (f *fakeAuth) Allowed(v *core.View, p auth.Principal, perm auth.Permission, element uuid.UUID) bool {
	for _, g := range f.Grants(p.UserID) {
		if g.Permission == perm && (g.ScopeElementID == uuid.Nil || (element != uuid.Nil && v.InScope(g.ScopeElementID, element))) {
			return true
		}
	}
	return false
}

// fakeCore hält eine feste View; Befehle nimmt nur der echte Core aus coretest an.
type fakeCore struct{ view atomic.Pointer[core.View] }

func (c *fakeCore) View() *core.View                        { return c.view.Load() }
func (*fakeCore) Subscribe(int) (<-chan core.Event, func()) { return nil, func() {} }
func (c *fakeCore) Do(context.Context, core.Command) error {
	return errors.New("fake core takes no commands")
}

// call schickt eine Anfrage durch den ganzen Handler, als Browser von monika.example.
func call(t *testing.T, s *Server, method, path, body, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequestWithContext(t.Context(), method, path, rd)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://monika.example")
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: cookie})
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestLoginFlow(t *testing.T) {
	s := newTestServer(t)
	tests := []struct {
		name, path, body string
		status           int
		want             string // Teil des Bodies
		cookie           string // Teil von Set-Cookie
	}{
		{"password", "/api/v1/auth/login", `{"username":"anna","password":"correct horse battery"}`, 200, `"challenge":"challenge","needs_enrollment":false`, ""},
		{"wrong password", "/api/v1/auth/login", `{"username":"anna","password":"wrong"}`, 401, `"code":"invalid_credentials"`, ""},
		{"throttled", "/api/v1/auth/login", `{"username":"locked","password":"x"}`, 429, `"code":"throttled"`, ""},
		{"code", "/api/v1/auth/login/totp", `{"challenge":"challenge","code":"123456"}`, 204, "", "monika_session=fresh; Path=/; Expires=Mon, 19 Oct 2026 12:00:00 GMT; HttpOnly; Secure; SameSite=Strict"},
		{"wrong code", "/api/v1/auth/login/totp", `{"challenge":"challenge","code":"000000"}`, 401, `"code":"invalid_code"`, ""},
		{"enroll", "/api/v1/auth/totp/enroll", `{"challenge":"challenge"}`, 200, `"otpauth_url":"otpauth://totp/Monika:anna"`, ""},
		{"confirm", "/api/v1/auth/totp/confirm", `{"challenge":"challenge","code":"123456"}`, 200, `"recovery_codes":["aaaaa-bbbbb"]`, "monika_session=fresh;"},
		{"kiosk", "/api/v1/auth/kiosk", `{"token":"monika_wall"}`, 204, "", "monika_session=kiosk;"},
		{"kiosk with a wrong token", "/api/v1/auth/kiosk", `{"token":"monika_x"}`, 401, `"code":"unauthenticated"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := call(t, s, "POST", tt.path, tt.body, "")
			if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.want) {
				t.Fatalf("response = %d %s, want %d with %s", w.Code, w.Body, tt.status, tt.want)
			}
			if got := w.Header().Get("Set-Cookie"); !strings.Contains(got, tt.cookie) || (tt.cookie == "" && got != "") {
				t.Errorf("Set-Cookie = %q, want %q", got, tt.cookie)
			}
		})
	}
	if w := call(t, s, "POST", "/api/v1/auth/login", `{"username":"locked","password":"x"}`, ""); w.Header().Get("Retry-After") != "30" {
		t.Errorf("Retry-After = %q", w.Header().Get("Retry-After"))
	}
	if m := s.cfg.Auth.(*fakeAuth).meta; m.RemoteAddr != "192.0.2.1" {
		t.Errorf("meta = %+v, want the address of the client", m)
	}
}

func TestOwnAccount(t *testing.T) {
	s := newTestServer(t)
	fa := s.cfg.Auth.(*fakeAuth)

	w := call(t, s, "GET", "/api/v1/auth/me", "", "anna")
	var me struct {
		Username string
		Grants   []struct {
			Permission     string
			ScopeElementID *string `json:"scope_element_id"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &me); w.Code != 200 || err != nil || me.Username != "anna" || len(me.Grants) != 2 ||
		me.Grants[0].ScopeElementID != nil || *me.Grants[1].ScopeElementID != scope.String() {
		t.Errorf("me = %d %s", w.Code, w.Body)
	}

	w = call(t, s, "GET", "/api/v1/auth/sessions", "", "anna")
	var sessions []struct {
		ID        string
		Current   bool
		UserAgent *string `json:"user_agent"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil || len(sessions) != 2 || !sessions[0].Current || sessions[1].Current ||
		*sessions[0].UserAgent != "Firefox" || sessions[1].UserAgent != nil {
		t.Errorf("sessions = %d %s", w.Code, w.Body)
	}
	if w := call(t, s, "DELETE", "/api/v1/auth/sessions/"+uuid.NewString(), "", "anna"); w.Code != 404 {
		t.Errorf("revoke unknown session = %d %s", w.Code, w.Body)
	}
	if w := call(t, s, "DELETE", "/api/v1/auth/sessions/"+anna.SessionID.String(), "", "anna"); w.Code != 204 {
		t.Errorf("revoke = %d %s", w.Code, w.Body)
	}

	if w := call(t, s, "PUT", "/api/v1/auth/password", `{"current_password":"wrong","new_password":"a new correct horse"}`, "anna"); w.Code != 422 ||
		!strings.Contains(w.Body.String(), `"code":"wrong_password"`) {
		t.Errorf("wrong current password = %d %s", w.Code, w.Body)
	}
	if w := call(t, s, "PUT", "/api/v1/auth/password", `{"current_password":"correct horse battery","new_password":"a new correct horse"}`, "anna"); w.Code != 204 {
		t.Errorf("change password = %d %s", w.Code, w.Body)
	}

	w = call(t, s, "POST", "/api/v1/auth/logout", "", "anna")
	if c := w.Header().Get("Set-Cookie"); w.Code != 204 || !strings.Contains(c, "monika_session=;") || !strings.Contains(c, "Max-Age=0") {
		t.Errorf("logout = %d, Set-Cookie %q", w.Code, c)
	}
	if len(fa.loggedOut) != 1 || fa.loggedOut[0] != "anna" {
		t.Errorf("logged out = %v", fa.loggedOut)
	}
}

func TestHealthz(t *testing.T) {
	s := newTestServer(t)
	fc := s.cfg.Core.(*fakeCore)
	for _, tt := range []struct {
		name   string
		view   *core.View
		status int
	}{
		{"core not running", nil, 503},
		{"database down", &core.View{DB: core.DBStatus{Error: "connection refused"}}, 503},
		{"ok", &core.View{DB: core.DBStatus{OK: true}}, 200},
	} {
		fc.view.Store(tt.view)
		if w := call(t, s, "GET", "/api/v1/healthz", "", ""); w.Code != tt.status || w.Body.Len() != 0 {
			t.Errorf("%s: %d %s", tt.name, w.Code, w.Body)
		}
	}
}

func TestClientIP(t *testing.T) {
	s := newTestServer(t) // vertraut 172.16.0.0/12
	tests := []struct {
		name, remote, forwarded, want string
	}{
		{"direct", "203.0.113.7:5000", "", "203.0.113.7"},
		{"direct ignores the header", "203.0.113.7:5000", "198.51.100.1", "203.0.113.7"},
		{"behind caddy", "172.18.0.5:5000", "198.51.100.1", "198.51.100.1"},
		{"client claims an address", "172.18.0.5:5000", "10.9.9.9, 198.51.100.1", "198.51.100.1"},
		{"two trusted proxies", "172.18.0.5:5000", "198.51.100.1, 172.18.0.9", "198.51.100.1"},
		{"proxy without header", "172.18.0.5:5000", "", "172.18.0.5"},
		{"garbage", "172.18.0.5:5000", "nonsense", "172.18.0.5"},
		{"ipv6 mapped", "[::ffff:203.0.113.7]:5000", "", "203.0.113.7"},
	}
	for _, tt := range tests {
		r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
		r.RemoteAddr = tt.remote
		if tt.forwarded != "" {
			r.Header.Set("X-Forwarded-For", tt.forwarded)
		}
		if got := s.clientIP(r); got != tt.want {
			t.Errorf("%s: clientIP = %s, want %s", tt.name, got, tt.want)
		}
	}
}
