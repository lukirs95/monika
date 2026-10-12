package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/sdktest"
	"github.com/lukirs95/monika/sdk/vocab"
)

// target ist die Attrappe des Ziels: Sie zeichnet Anfragen auf und antwortet pro Pfad mit dem
// eingestellten Status (Standard 204).
type target struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recorded
	status   map[string]int
	delay    time.Duration
}

type recorded struct {
	method, path, rawPath, query, body string
	header                             nethttp.Header
}

func newTarget(t *testing.T) *target {
	t.Helper()
	tg := &target{status: map[string]int{}}
	tg.Server = httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		b, _ := io.ReadAll(r.Body)
		tg.mu.Lock()
		tg.requests = append(tg.requests, recorded{r.Method, r.URL.Path, r.URL.EscapedPath(), r.URL.RawQuery, string(b), r.Header.Clone()})
		status, delay := tg.status[r.URL.Path], tg.delay
		tg.mu.Unlock()
		time.Sleep(delay)
		if status == nethttp.StatusFound {
			w.Header().Set("Location", "/elsewhere")
		}
		if status == 0 {
			status = nethttp.StatusNoContent
		}
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = io.WriteString(w, `{"error":"busy"}`)
		}
	}))
	t.Cleanup(tg.Close)
	return tg
}

func (tg *target) set(path string, status int) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.status[path] = status
}

func (tg *target) last() recorded {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return tg.requests[len(tg.requests)-1]
}

func (tg *target) count() int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return len(tg.requests)
}

const tallyConfig = `{
  "timeout_s": 1,
  "calls": [
    {"id": "tally", "name": "Tally Kamera", "method": "POST", "path": "/api/tally/{{camera}}",
     "query": {"state": "{{state}}", "source": "monika"},
     "headers": {"X-Origin": "monika"},
     "body": "{\"camera\": {{camera}}, \"state\": {{state}}, \"label\": {{label}}}",
     "params": {
       "camera": {"type": "int", "min": 1, "max": 16, "default": 3},
       "state":  {"type": "enum", "values": ["off", "preview", "program"], "default": "off"},
       "label":  {"type": "string", "default": "Cam A/B"}
     }},
    {"id": "rec", "name": "Aufnahme", "method": "GET", "path": "/rec", "critical": true}
  ]
}`

func newTestSource(t *testing.T, address, cfg, secret string) *source {
	t.Helper()
	sc := sdk.SourceConfig{Address: address, Config: json.RawMessage(cfg), Logger: slog.New(slog.DiscardHandler), Scheduler: sdktest.Scheduler{}}
	if secret != "" {
		sc.Secret = json.RawMessage(secret)
	}
	src, err := newSource(sc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return src.(*source)
}

func TestConfig(t *testing.T) {
	call := func(extra string) string {
		return `{"calls": [{"id": "a", "name": "A", "method": "POST", "path": "/a"` + extra + `}]}`
	}
	tests := []struct {
		name, address, config, secret, want string
	}{
		{"valid", "http://h:8000", tallyConfig, "", ""},
		{"base path", "https://hooks.example/monika/", call(""), "", ""},
		{"no scheme", "h:8000", call(""), "", "want a base URL"},
		{"query in the address", "http://h/?x=1", call(""), "", "want a base URL"},
		{"id twice", "http://h", `{"calls": [{"id": "a", "name": "A", "method": "GET", "path": "/a"}, {"id": "a", "name": "B", "method": "GET", "path": "/b"}]}`, "", "used twice"},
		{"unknown placeholder", "http://h", call(`, "body": "{{nope}}"`), "", "unknown placeholder {{nope}}"},
		{"unclosed placeholder", "http://h", call(`, "body": "{{nope"`), "", "unclosed placeholder"},
		{"query in the path", "http://h", `{"calls": [{"id": "a", "name": "A", "method": "GET", "path": "/a?x=1"}]}`, "", "without ?"},
		{"body is no JSON", "http://h", call(`, "body": "{\"x\": {{v}}", "params": {"v": {"type": "int"}}`), "", "no valid JSON"},
		{"default out of range", "http://h", call(`, "params": {"v": {"type": "int", "max": 3, "default": 5}}`), "", "out of range"},
		{"enum without values", "http://h", call(`, "params": {"v": {"type": "enum"}}`), "", "enum needs values"},
		{"secret is no object", "http://h", call(""), `"geheim"`, "secret: want a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := sdk.SourceConfig{Address: tt.address, Config: json.RawMessage(tt.config), Logger: slog.New(slog.DiscardHandler)}
			if tt.secret != "" {
				sc.Secret = json.RawMessage(tt.secret)
			}
			_, err := newSource(sc)
			if (tt.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.want)) {
				t.Errorf("New = %v, want %q", err, tt.want)
			}
			if err != nil && strings.Contains(err.Error(), "geheim") {
				t.Error("secret in the error")
			}
		})
	}
}

func TestTrigger(t *testing.T) {
	tg := newTarget(t)
	s := newTestSource(t, tg.URL, tallyConfig, `{"username": "monika", "password": "pw", "headers": {"X-Origin": "secret", "X-Token": "t0k3n"}}`)
	ctx := t.Context()

	res, err := s.Execute(ctx, sdk.Command{ExternalID: "tally", Action: vocab.ActionTrigger, Params: json.RawMessage(`{"camera": 12, "state": "program"}`)})
	if err != nil || res.Status != sdk.Accepted || string(res.Response) != `{"status":204}` {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	r := tg.last()
	user, pass, _ := (&nethttp.Request{Header: r.header}).BasicAuth()
	if r.method != "POST" || r.path != "/api/tally/12" || r.query != "source=monika&state=program" ||
		r.body != `{"camera": 12, "state": "program", "label": "Cam A/B"}` || r.header.Get("Content-Type") != "application/json" ||
		r.header.Get("X-Origin") != "secret" || r.header.Get("X-Token") != "t0k3n" || user != "monika" || pass != "pw" {
		t.Errorf("request = %+v", r)
	}

	// Ein Pfadabschnitt wird kodiert; ein / im Wert trennt keine Abschnitte.
	named := newTestSource(t, tg.URL+"/base", `{"calls": [{"id": "n", "name": "N", "method": "PUT", "path": "/names/{{label}}",
		"params": {"label": {"type": "string"}}}]}`, "")
	if res, err := named.Execute(ctx, sdk.Command{ExternalID: "n", Action: vocab.ActionTrigger, Params: json.RawMessage(`{"label": "Cam A/B"}`)}); err != nil || res.Status != sdk.Accepted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	if r := tg.last(); r.rawPath != "/base/names/Cam%20A%2FB" || r.body != "" {
		t.Errorf("path = %s, body %q", r.rawPath, r.body)
	}
	if res, _ := named.Execute(ctx, sdk.Command{ExternalID: "n", Action: vocab.ActionTrigger}); res.Status != sdk.Rejected || !slices.Equal(res.RejectedFields, []string{"label"}) {
		t.Errorf("missing without default = %+v", res)
	}

	rejected := []struct {
		name, params string
		fields       []string
	}{
		{"unknown", `{"zoom": 2}`, []string{"zoom"}},
		{"out of range", `{"camera": 17}`, []string{"camera"}},
		{"not an integer", `{"camera": 1.5}`, []string{"camera"}},
		{"not in values", `{"state": "live"}`, []string{"state"}},
		{"wrong type", `{"label": 3, "state": true}`, []string{"label", "state"}},
		{"no object", `[1]`, []string{"params"}},
	}
	before := tg.count()
	for _, tt := range rejected {
		res, err := s.Execute(ctx, sdk.Command{ExternalID: "tally", Action: vocab.ActionTrigger, Params: json.RawMessage(tt.params)})
		if err != nil || res.Status != sdk.Rejected || !slices.Equal(res.RejectedFields, tt.fields) {
			t.Errorf("%s: Execute = %+v, %v", tt.name, res, err)
		}
	}
	if tg.count() != before {
		t.Error("rejected parameters were sent")
	}

	tg.set("/rec", nethttp.StatusServiceUnavailable)
	res, err = s.Execute(ctx, sdk.Command{ExternalID: "rec", Action: vocab.ActionTrigger})
	if err != nil || res.Status != sdk.Rejected || string(res.Response) != `{"body":"{\"error\":\"busy\"}","status":503}` {
		t.Errorf("503 = %+v, %v", res, err)
	}
	tg.set("/rec", nethttp.StatusFound)
	n := tg.count()
	if res, err := s.Execute(ctx, sdk.Command{ExternalID: "rec", Action: vocab.ActionTrigger}); err != nil || res.Status != sdk.Rejected || tg.count() != n+1 {
		t.Errorf("redirect = %+v, %v, %d requests; want rejected without following", res, err, tg.count()-n)
	}
	tg.mu.Lock()
	tg.delay = 1500 * time.Millisecond
	tg.mu.Unlock()
	if _, err := s.Execute(ctx, sdk.Command{ExternalID: "rec", Action: vocab.ActionTrigger}); err == nil || strings.Contains(err.Error(), "pw") {
		t.Errorf("timeout = %v, want an error without the password", err)
	}
	if _, err := s.Execute(ctx, sdk.Command{ExternalID: "nope", Action: vocab.ActionTrigger}); err == nil {
		t.Error("unknown call accepted")
	}
}

// noScheduler ruft nie auf; die Tests melden Health-Checks selbst über s.checked.
type noScheduler struct{}

func (noScheduler) Every(context.Context, time.Duration, func(context.Context) error) {}

func TestRun(t *testing.T) {
	tg := newTarget(t)
	cfg := strings.Replace(tallyConfig, `"timeout_s": 1,`, `"timeout_s": 1, "health": {"path": "/health", "interval_s": 5},`, 1)
	s := newTestSource(t, tg.URL, cfg, "")
	s.sched = noScheduler{}
	if s.Heartbeat() != 5*time.Second {
		t.Errorf("Heartbeat = %v", s.Heartbeat())
	}
	r := sdktest.NewReporter()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { done <- s.Run(ctx, r) }()
	wait := func(from int, pred func(sdktest.Call) bool) (sdktest.Call, int) {
		t.Helper()
		wctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		c, i, err := r.WaitFor(wctx, from, pred)
		if err != nil {
			t.Fatalf("waiting: %v; calls %+v", err, r.Calls())
		}
		return c, i
	}

	// Bis zum ersten erfolgreichen Health-Check bleibt die Quelle getrennt; Fakten merkt sie sich.
	if _, err := s.Execute(t.Context(), sdk.Command{ExternalID: "rec", Action: vocab.ActionTrigger}); err != nil {
		t.Fatal(err)
	}
	s.checked <- nil
	_, i := wait(0, sdktest.Is(sdktest.CallConnected))
	c, i := wait(i, sdktest.Is(sdktest.CallSnapshot))
	snap := c.Snapshot
	if len(snap.Elements) != 2 || snap.Facts["rec"]["call.ok"] != true || snap.Facts["rec"]["http.status_code"] != int64(204) {
		t.Errorf("snapshot = %+v", snap)
	}
	if e := snap.Elements[1]; !slices.Equal(e.CriticalActions, []string{"trigger"}) || e.Kind != "trigger" {
		t.Errorf("rec = %+v", e)
	}
	if p := snap.Elements[0].Params["trigger"]["camera"]; p.Default != int64(3) || *p.Max != 16 {
		t.Errorf("params = %+v", snap.Elements[0].Params)
	}

	tg.set("/rec", nethttp.StatusInternalServerError)
	_, _ = s.Execute(t.Context(), sdk.Command{ExternalID: "rec", Action: vocab.ActionTrigger})
	c, i = wait(i, sdktest.Is(sdktest.CallUpdate))
	if f := c.Update.Facts["rec"]; f["call.ok"] != false || f["http.status_code"] != int64(500) || f["call.called_at"] == "" {
		t.Errorf("update = %+v", c.Update)
	}
	s.checked <- nil
	_, i = wait(i, sdktest.Is(sdktest.CallHeartbeat))
	s.checked <- io.ErrUnexpectedEOF
	_, i = wait(i, sdktest.Is(sdktest.CallDisconnected))
	s.checked <- nil
	_, i = wait(i, sdktest.Is(sdktest.CallConnected))
	c, _ = wait(i, sdktest.Is(sdktest.CallSnapshot))
	if c.Snapshot.Facts["rec"]["http.status_code"] != int64(500) {
		t.Errorf("snapshot after reconnect = %+v", c.Snapshot.Facts)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v", err)
	}
}

func TestHealthCheck(t *testing.T) {
	tg := newTarget(t)
	cfg := `{"health": {"path": "/health", "interval_s": 1}, "calls": [{"id": "a", "name": "A", "method": "GET", "path": "/a"}]}`
	s := newTestSource(t, tg.URL, cfg, "")
	if err := s.checkHealth(t.Context()); err != nil || tg.last().method != "GET" || tg.last().path != "/health" {
		t.Errorf("healthy = %v, %+v", err, tg.last())
	}
	<-s.checked
	tg.set("/health", nethttp.StatusServiceUnavailable)
	if err := s.checkHealth(t.Context()); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("unhealthy = %v", err)
	}
}

func TestConformance(t *testing.T) {
	d, ok := sdk.Lookup(Name)
	if !ok {
		t.Fatal("driver not registered")
	}
	commands := []sdk.Command{
		{ExternalID: "tally", Action: vocab.ActionTrigger, Params: json.RawMessage(`{"camera": 2}`)},
		{ExternalID: "rec", Action: vocab.ActionTrigger},
		{ExternalID: "tally", Action: vocab.ActionTrigger},
	}
	for _, health := range []string{"", `"health": {"path": "/health", "interval_s": 1},`} {
		tg := newTarget(t)
		cfg := strings.Replace(tallyConfig, `"timeout_s": 1,`, `"timeout_s": 1, `+health, 1)
		sdktest.Conformance(t, d, sdk.SourceConfig{Address: tg.URL, Config: json.RawMessage(cfg)}, sdktest.Options{Commands: commands})
	}
}
