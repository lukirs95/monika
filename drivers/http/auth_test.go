package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/sdktest"
	"github.com/lukirs95/monika/sdk/vocab"
)

// loginTarget ist ein Ziel mit Anmeldung: /login prüft die Zugangsdaten und vergibt einen Token,
// /x verlangt ihn als Cookie session oder Header Authorization: Bearer.
type loginTarget struct {
	*httptest.Server
	mu          sync.Mutex
	valid       string // gültiger Token; leer = keiner (abgelaufen)
	logins      int
	calls       int    // Anfragen an /x
	loginStatus int    // ≠ 0: Anmeldung abgelehnt
	deliver     string // cookie, header, json oder none
	loginDelay  time.Duration
}

func newLoginTarget(t *testing.T, deliver string) *loginTarget {
	t.Helper()
	lt := &loginTarget{deliver: deliver}
	lt.Server = httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		lt.mu.Lock()
		defer lt.mu.Unlock()
		switch r.URL.Path {
		case "/login":
			lt.logins++
			lt.mu.Unlock()
			time.Sleep(lt.loginDelay)
			lt.mu.Lock()
			b, _ := io.ReadAll(r.Body)
			if lt.loginStatus != 0 {
				w.WriteHeader(lt.loginStatus)
				_, _ = fmt.Fprintf(w, "wrong password: %s", b) // Echo, darf nicht weitergegeben werden
				return
			}
			if string(b) != `{"user": "monika", "password": "pw-geheim"}` {
				w.WriteHeader(nethttp.StatusForbidden)
				return
			}
			lt.valid = fmt.Sprintf("tok-%d", lt.logins)
			switch lt.deliver {
			case "cookie":
				nethttp.SetCookie(w, &nethttp.Cookie{Name: "session", Value: lt.valid})
			case "header":
				w.Header().Set("X-Auth-Token", lt.valid)
			case "json":
				_, _ = fmt.Fprintf(w, `{"data": {"token": %q}}`, lt.valid)
			}
		case "/x":
			lt.calls++
			got := r.Header.Get("Authorization")
			if c, err := r.Cookie("session"); err == nil {
				got = "Bearer " + c.Value
			}
			if lt.valid == "" || got != "Bearer "+lt.valid {
				w.WriteHeader(nethttp.StatusUnauthorized)
				return
			}
			w.WriteHeader(nethttp.StatusNoContent)
		case "/always":
			w.WriteHeader(nethttp.StatusUnauthorized)
		}
	}))
	t.Cleanup(lt.Close)
	return lt
}

func (lt *loginTarget) counts() (logins, calls int) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	return lt.logins, lt.calls
}

func (lt *loginTarget) with(f func()) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	f()
}

const loginSecret = `{"user": "monika", "password": "pw-geheim"}`

func authConfig(token, apply, login string) string {
	return `{"timeout_s": 2, "auth": {"login": "` + login + `", "path": "/login",
	  "body": "{\"user\": {{secret.user}}, \"password\": {{secret.password}}}",
	  "token": ` + token + `, "apply": ` + apply + `},
	  "calls": [{"id": "x", "name": "X", "method": "POST", "path": "/x"}, {"id": "always", "name": "A", "method": "GET", "path": "/always"}]}`
}

const bearer = `{"as": "header", "name": "Authorization", "format": "Bearer {{token}}"}`

func trigger(t *testing.T, s *source, id string) sdk.Result {
	t.Helper()
	res, err := s.Execute(t.Context(), sdk.Command{ExternalID: id, Action: vocab.ActionTrigger})
	if err != nil {
		t.Fatalf("Execute %s: %v", id, err)
	}
	return res
}

func TestAuthTokens(t *testing.T) {
	tests := []struct {
		name, deliver, token, apply string
	}{
		{"cookie to cookie", "cookie", `{"from": "cookie", "name": "session"}`, `{"as": "cookie", "name": "session"}`},
		{"header to bearer", "header", `{"from": "header", "name": "X-Auth-Token"}`, bearer},
		{"json pointer to bearer", "json", `{"from": "json", "pointer": "/data/token"}`, bearer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lt := newLoginTarget(t, tt.deliver)
			s := newTestSource(t, lt.URL, authConfig(tt.token, tt.apply, "on_demand"), loginSecret)

			// Erster Aufruf ohne Token: 401, Anmeldung, genau eine Wiederholung.
			if res := trigger(t, s, "x"); res.Status != sdk.Accepted {
				t.Fatalf("first call = %+v", res)
			}
			if logins, calls := lt.counts(); logins != 1 || calls != 2 {
				t.Errorf("first call: %d logins, %d calls", logins, calls)
			}
			// Der Token bleibt im Speicher.
			trigger(t, s, "x")
			if logins, calls := lt.counts(); logins != 1 || calls != 3 {
				t.Errorf("second call: %d logins, %d calls", logins, calls)
			}
			// Abgelaufen: beim nächsten Aufruf neu anmelden.
			lt.with(func() { lt.valid = "" })
			if res := trigger(t, s, "x"); res.Status != sdk.Accepted {
				t.Errorf("after expiry = %+v", res)
			}
			if logins, _ := lt.counts(); logins != 2 {
				t.Errorf("after expiry: %d logins", logins)
			}
		})
	}
}

func TestAuthFailures(t *testing.T) {
	lt := newLoginTarget(t, "json")
	s := newTestSource(t, lt.URL, authConfig(`{"from": "json", "pointer": "/data/token"}`, bearer, "on_demand"), loginSecret)

	// Auch die Wiederholung wird abgelehnt: rejected, keine Schleife.
	res := trigger(t, s, "always")
	if logins, _ := lt.counts(); res.Status != sdk.Rejected || logins != 1 || !strings.Contains(string(res.Response), `"status":401`) {
		t.Errorf("always 401 = %+v, %d logins", res, logins)
	}

	// Anmeldung abgelehnt: auth true, ohne Body (er wiederholt das Passwort).
	lt.with(func() { lt.valid, lt.loginStatus = "", nethttp.StatusForbidden })
	res = trigger(t, s, "x")
	if res.Status != sdk.Rejected || string(res.Response) != `{"auth":true,"status":403}` ||
		strings.Contains(res.Detail+string(res.Response), "geheim") {
		t.Errorf("login rejected = %+v", res)
	}

	// Kein Token in der Antwort.
	lt.with(func() { lt.loginStatus, lt.deliver = 0, "none" })
	if res := trigger(t, s, "x"); res.Status != sdk.Rejected || !strings.Contains(res.Detail, "no token") {
		t.Errorf("no token = %+v", res)
	}
}

func TestAuthParallel(t *testing.T) {
	lt := newLoginTarget(t, "cookie")
	lt.loginDelay = 50 * time.Millisecond
	s := newTestSource(t, lt.URL, authConfig(`{"from": "cookie", "name": "session"}`, `{"as": "cookie", "name": "session"}`, "on_demand"), loginSecret)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if res := trigger(t, s, "x"); res.Status != sdk.Accepted {
				t.Errorf("parallel call = %+v", res)
			}
		})
	}
	wg.Wait()
	if logins, _ := lt.counts(); logins != 1 {
		t.Errorf("logins = %d, want one for all parallel calls", logins)
	}
}

func TestAuthOnStart(t *testing.T) {
	var logs bytes.Buffer
	lt := newLoginTarget(t, "header")
	lt.loginStatus = nethttp.StatusInternalServerError
	sc := sdk.SourceConfig{Address: lt.URL, Config: json.RawMessage(authConfig(`{"from": "header", "name": "X-Auth-Token"}`, bearer, "on_start")),
		Secret: json.RawMessage(loginSecret), Logger: slog.New(slog.NewTextHandler(&logs, nil)), Scheduler: sdktest.Scheduler{}}
	src, err := newSource(sc)
	if err != nil {
		t.Fatal(err)
	}
	s := src.(*source)
	s.retryLogin = 20 * time.Millisecond
	r := sdktest.NewReporter()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { done <- s.Run(ctx, r) }()
	wctx, wcancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer wcancel()

	// Die erste Anmeldung scheitert: getrennt, bis sie gelingt; dann verbunden mit Token.
	if _, _, err := r.WaitFor(wctx, 0, sdktest.Is(sdktest.CallDisconnected)); err != nil {
		t.Fatalf("no Disconnected: %v", err)
	}
	lt.with(func() { lt.loginStatus = 0 })
	if _, _, err := r.WaitFor(wctx, 0, sdktest.Is(sdktest.CallConnected)); err != nil {
		t.Fatalf("no Connected: %v", err)
	}
	if res := trigger(t, s, "x"); res.Status != sdk.Accepted {
		t.Errorf("call after login = %+v", res)
	}
	if _, calls := lt.counts(); calls != 1 {
		t.Errorf("calls = %d, want the first call with a token", calls)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v", err)
	}
	if out := logs.String(); strings.Contains(out, "geheim") || strings.Contains(out, "tok-") {
		t.Errorf("secret or token in the log:\n%s", out)
	}
}

func TestAuthConfig(t *testing.T) {
	cfg := func(auth string) string {
		return `{"auth": ` + auth + `, "calls": [{"id": "x", "name": "X", "method": "GET", "path": "/x"}]}`
	}
	tests := []struct {
		name, auth, secret, want string
	}{
		{"valid", `{"path": "/login", "body": "{\"u\": {{secret.user}}}", "token": {"from": "cookie", "name": "s"}, "apply": {"as": "cookie", "name": "s"}}`, loginSecret, ""},
		{"field missing in the secret", `{"path": "/login", "body": "{\"u\": {{secret.nope}}}", "token": {"from": "cookie", "name": "s"}, "apply": {"as": "cookie", "name": "s"}}`, loginSecret, "no such field"},
		{"no secret placeholder", `{"path": "/login", "body": "{{user}}", "token": {"from": "cookie", "name": "s"}, "apply": {"as": "cookie", "name": "s"}}`, loginSecret, "unknown placeholder"},
		{"format without token", `{"path": "/login", "token": {"from": "cookie", "name": "s"}, "apply": {"as": "header", "name": "A", "format": "Bearer"}}`, "", "needs {{token}}"},
		{"cookie without name", `{"path": "/login", "token": {"from": "cookie"}, "apply": {"as": "cookie", "name": "s"}}`, "", "name missing"},
		{"pointer without slash", `{"path": "/login", "token": {"from": "json", "pointer": "token"}, "apply": {"as": "cookie", "name": "s"}}`, "", "pointer must start with /"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := sdk.SourceConfig{Address: "http://h", Config: json.RawMessage(cfg(tt.auth)), Logger: slog.New(slog.DiscardHandler)}
			if tt.secret != "" {
				sc.Secret = json.RawMessage(tt.secret)
			}
			_, err := newSource(sc)
			if (tt.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.want)) || (err != nil && strings.Contains(err.Error(), "geheim")) {
				t.Errorf("New = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestJSONPointer(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"a": {"b/c": [{"d~e": "x"}]}}`), &doc)
	if v, err := jsonPointer(doc, "/a/b~1c/0/d~0e"); err != nil || v != "x" {
		t.Errorf("pointer = %v, %v", v, err)
	}
	for _, p := range []string{"/a/x", "/a/b~1c/1", "/a/b~1c/0/d~0e/z"} {
		if _, err := jsonPointer(doc, p); err == nil {
			t.Errorf("%s: no error", p)
		}
	}
}
