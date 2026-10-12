//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP nach RFC 6238 verlangt SHA-1
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// tallyTarget ist das Ziel des HTTP-Treibers: Anmeldung über /login (Token im JSON), Tally über
// /tally/{camera} mit Bearer-Token, Health-Check über /health.
type tallyTarget struct {
	*httptest.Server
	mu     sync.Mutex
	token  string // gültiger Token; leer = abgelaufen
	logins int
	status int // Antwort auf /tally, 0 = 204
	last   string
}

func newTallyTarget(t *testing.T) *tallyTarget {
	t.Helper()
	tt := &tallyTarget{}
	tt.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tt.mu.Lock()
		defer tt.mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/login":
			if string(b) != `{"user": "monika", "password": "pw-geheim"}` {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			tt.logins++
			tt.token = fmt.Sprintf("tok-%d", tt.logins)
			_, _ = fmt.Fprintf(w, `{"token": %q}`, tt.token)
		case strings.HasPrefix(r.URL.Path, "/tally/"):
			if tt.token == "" || r.Header.Get("Authorization") != "Bearer "+tt.token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			tt.last = r.URL.Path + " " + string(b)
			if tt.status != 0 {
				w.WriteHeader(tt.status)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(tt.Close)
	return tt
}

func (tt *tallyTarget) with(f func()) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	f()
}

// client spricht die API wie ein Browser: Sitzungs-Cookie und Origin.
type client struct {
	t       *testing.T
	base    string
	session string
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(c.t.Context(), method, c.base+"/api/v1"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.base)
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: "monika_session", Value: c.session})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	for _, ck := range resp.Cookies() {
		if ck.Name == "monika_session" {
			c.session = ck.Value
		}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
	if resp.StatusCode >= 300 {
		c.t.Logf("%s %s = %d %s", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode
}

// must ruft auf und erwartet den Status.
func (c *client) must(want int, method, path string, body, out any) {
	c.t.Helper()
	if got := c.do(method, path, body, out); got != want {
		c.t.Fatalf("%s %s = %d, want %d", method, path, got, want)
	}
}

// totp berechnet den aktuellen Code (RFC 6238, SHA-1, 6 Stellen, 30 s).
func totp(t *testing.T, secret string) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("totp secret: %v", err)
	}
	mac := hmac.New(sha1.New, key)
	_ = binary.Write(mac, binary.BigEndian, time.Now().Unix()/30)
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// eventually wartet, bis ok gilt.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); !ok(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
	}
}

// Monika mit dem HTTP-Treiber von Ende zu Ende: Anmeldung samt TOTP, Gerät, Secret und Quelle
// über die API; die Quelle wird Live, trigger erreicht das Ziel mit Anmeldung, eine Regel auf
// call.ok wird verletzt und wieder erfüllt, ein abgelaufener Token wird erneuert.
func TestHTTPDriverEndToEnd(t *testing.T) {
	target := newTallyTarget(t)
	addr := freePort(t)
	environ := []string{
		"MONIKA_ENV_FILE=" + t.TempDir() + "/none.env",
		"MONIKA_DATABASE_URL=" + server.NewDBURL(t),
		"MONIKA_MASTER_KEY=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		"MONIKA_PUBLIC_URL=http://" + addr,
		"MONIKA_LISTEN=" + addr,
		"MONIKA_ADMIN_USER=admin",
		"MONIKA_ADMIN_PASSWORD=correct horse battery",
		"MONIKA_LOG_FORMAT=text",
	}
	ctx, cancel := context.WithCancel(t.Context())
	logs := &syncBuffer{}
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, environ, logs, logs) }()
	t.Cleanup(func() {
		cancel()
		if code := <-done; code != 0 {
			t.Errorf("exit code %d", code)
		}
		if strings.Contains(logs.String(), "pw-geheim") || strings.Contains(logs.String(), "tok-") {
			t.Errorf("secret or token in the log:\n%s", logs)
		}
	})
	eventually(t, "healthy", func() bool { return run(t.Context(), []string{"-healthcheck"}, environ, io.Discard, io.Discard) == 0 })

	// Anmeldung mit Einrichtung von TOTP.
	c := &client{t: t, base: "http://" + addr}
	var step struct{ Challenge string }
	c.must(200, "POST", "/auth/login", map[string]string{"username": "admin", "password": "correct horse battery"}, &step)
	var enroll struct{ Secret string }
	c.must(200, "POST", "/auth/totp/enroll", map[string]string{"challenge": step.Challenge}, &enroll)
	c.must(200, "POST", "/auth/totp/confirm", map[string]string{"challenge": step.Challenge, "code": totp(t, enroll.Secret)}, nil)

	// Gerät, Secret und Quelle.
	var device, secret, source struct {
		ID              string
		AccessElementID string `json:"access_element_id"`
	}
	c.must(201, "POST", "/devices", map[string]any{"label": "Tally"}, &device)
	c.must(201, "POST", "/secrets", map[string]any{"name": "tally", "value": map[string]string{"user": "monika", "password": "pw-geheim"}}, &secret)
	config := map[string]any{
		"timeout_s": 2,
		"health":    map[string]any{"path": "/health", "interval_s": 1},
		"auth": map[string]any{
			"path":  "/login",
			"body":  `{"user": {{secret.user}}, "password": {{secret.password}}}`,
			"token": map[string]string{"from": "json", "pointer": "/token"},
			"apply": map[string]string{"as": "header", "name": "Authorization", "format": "Bearer {{token}}"},
		},
		"calls": []any{map[string]any{
			"id": "tally", "name": "Tally Kamera", "method": "POST", "path": "/tally/{{camera}}",
			"body": `{"state": {{state}}}`,
			"params": map[string]any{
				"camera": map[string]any{"type": "int", "min": 1, "max": 16, "default": 1},
				"state":  map[string]any{"type": "enum", "values": []string{"off", "program"}, "default": "off"},
			},
		}},
	}
	c.must(201, "POST", "/devices/"+device.ID+"/sources", map[string]any{
		"driver": "http", "address": target.URL, "config": config, "secret_id": secret.ID, "enabled": true,
	}, &source)

	// Die Quelle wird Live, das Aufruf-Element erscheint.
	var tally string
	eventually(t, "source live", func() bool {
		var st struct {
			Elements []struct {
				ID         string
				ExternalID string `json:"external_id"`
			}
			Sources []struct {
				SourceID string `json:"source_id"`
				State    string
			}
		}
		c.must(200, "GET", "/state", nil, &st)
		for _, e := range st.Elements {
			if e.ExternalID == "tally" {
				tally = e.ID
			}
		}
		for _, s := range st.Sources {
			if s.SourceID == source.ID && s.State == "live" {
				return tally != ""
			}
		}
		return false
	})
	c.must(201, "POST", "/rules", map[string]any{
		"name": "Tally erreichbar", "enabled": true, "severity": "warning", "scope_element_id": tally,
		"fact": "call.ok", "op": "eq", "value": true, "hold_seconds": 0,
	}, nil)

	trigger := func(params map[string]any) string {
		t.Helper()
		var accepted struct {
			RunID string `json:"run_id"`
		}
		c.must(202, "POST", "/runs", map[string]any{"element_id": tally, "action": "trigger", "params": params}, &accepted)
		var r struct {
			Status     string
			FinishedAt *time.Time `json:"finished_at"`
		}
		eventually(t, "run finished", func() bool {
			c.must(200, "GET", "/runs/"+accepted.RunID, nil, &r)
			return r.FinishedAt != nil
		})
		return r.Status
	}
	findings := func() int {
		var st struct {
			Findings []struct {
				ElementID string `json:"element_id"`
			}
		}
		c.must(200, "GET", "/state", nil, &st)
		n := 0
		for _, f := range st.Findings {
			if f.ElementID == tally {
				n++
			}
		}
		return n
	}

	// Erster Aufruf: Anmeldung, Parameter eingesetzt, accepted, call.ok in den Fakten.
	if status := trigger(map[string]any{"camera": 5, "state": "program"}); status != "accepted" {
		t.Fatalf("run = %s", status)
	}
	target.with(func() {
		if target.last != `/tally/5 {"state": "program"}` || target.logins != 1 {
			t.Errorf("target got %q after %d logins", target.last, target.logins)
		}
	})
	var facts struct{ Facts map[string]any }
	c.must(200, "GET", "/elements/"+tally+"/facts", nil, &facts)
	if facts.Facts["call.ok"] != true || facts.Facts["http.status_code"] != 204.0 {
		t.Errorf("facts = %v", facts.Facts)
	}

	// Das Ziel lehnt ab: rejected, die Regel wird verletzt.
	target.with(func() { target.status = http.StatusServiceUnavailable })
	if status := trigger(nil); status != "rejected" {
		t.Errorf("run against 503 = %s", status)
	}
	eventually(t, "finding raised", func() bool { return findings() == 1 })

	// Wieder in Ordnung, aber der Token ist abgelaufen: neu anmelden, Befund endet.
	target.with(func() { target.status, target.token = 0, "" })
	if status := trigger(nil); status != "accepted" {
		t.Errorf("run after expiry = %s", status)
	}
	eventually(t, "finding ended", func() bool { return findings() == 0 })
	target.with(func() {
		if target.logins != 2 {
			t.Errorf("logins = %d, want a second after expiry", target.logins)
		}
	})
}
