package forward

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
)

var (
	at        = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	public, _ = url.Parse("https://monika.example")
	findingX  = evaluation.Finding{
		ID: uuid.MustParse("00000000-0000-0000-0000-0000000000f1"), ElementID: uuid.MustParse("00000000-0000-0000-0000-0000000000e1"),
		ElementPath: "X8 / SDI 1", Origin: evaluation.OriginRule, Code: "rule.violated", Severity: evaluation.SeverityError,
		Message: "temp lte 80", RaisedAt: at,
	}
)

func TestMessageFormat(t *testing.T) {
	m := findingMessage(EventFindingRaised, at, findingX, "", public)
	b, _ := json.Marshal(m)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	el := got["element"].(map[string]any)
	f := got["finding"].(map[string]any)
	if got["event"] != "finding.raised" || el["path"] != "X8 / SDI 1" || el["url"] != "https://monika.example/elements/"+findingX.ElementID.String() ||
		f["severity"] != "error" || f["code"] != "rule.violated" || got["run"] != nil || f["cleared_at"] != nil {
		t.Errorf("message = %s", b)
	}
	tests := []struct {
		m    message
		want int
	}{
		{m, 3},
		{findingMessage(EventFindingWithdrawn, at, findingX, evaluation.ReasonDeleted, public), 5},
		{findingMessage(EventFindingAcked, at, findingX, "", public), 6},
		{runMessage(at, action.Run{Status: action.StatusFailed}, public), 4},
		{runMessage(at, action.Run{Status: action.StatusConfirmed}, public), 6},
	}
	for _, tt := range tests {
		if got := tt.m.syslogSeverity(); got != tt.want {
			t.Errorf("%s: severity %d, want %d", tt.m.Event, got, tt.want)
		}
	}
}

func TestRender(t *testing.T) {
	event := map[string]any{"finding": map[string]any{"severity": "error", "code": "a b"}, "count": 3.0}
	secret := map[string]any{"token": "s3cret"}
	tests := []struct {
		tpl   string
		where placement
		want  string
	}{
		{`{"text": {{finding.severity}}, "n": {{count}}, "x": {{missing}}}`, inBody, `{"text": "error", "n": 3, "x": null}`},
		{`https://h/{{finding.code}}?t={{secret.token}}`, inURL, `https://h/a%20b?t=s3cret`},
		{`Bearer {{secret.token}}`, inHeader, `Bearer s3cret`},
	}
	for _, tt := range tests {
		got, err := render(tt.tpl, tt.where, event, secret)
		if err != nil || got != tt.want {
			t.Errorf("render(%s) = %q, %v; want %q", tt.tpl, got, err, tt.want)
		}
	}
	if _, err := render(`{{secret.token}}`, inBody, event, secret); err == nil {
		t.Error("secret in body accepted")
	}
}

func TestValidate(t *testing.T) {
	valid := func(kind Kind, config string) Target {
		return Target{ID: uuid.New(), Name: "t", Kind: kind, Config: json.RawMessage(config), Events: []string{EventFindingRaised}}
	}
	tests := []struct {
		name string
		t    Target
		ok   bool
	}{
		{"syslog", valid(KindSyslog, `{"host":"loki"}`), true},
		{"webhook", valid(KindWebhook, `{"url":"https://h/{{secret.path}}","headers":{"Authorization":"Bearer {{secret.token}}"},"body":"{\"t\": {{finding.code}}}"}`), true},
		{"syslog without host", valid(KindSyslog, `{}`), false},
		{"unknown protocol", valid(KindSyslog, `{"host":"h","protocol":"sctp"}`), false},
		{"unknown facility", valid(KindSyslog, `{"host":"h","facility":"local9"}`), false},
		{"webhook without url", valid(KindWebhook, `{}`), false},
		{"secret in body", valid(KindWebhook, `{"url":"https://h","body":"{{secret.token}}"}`), false},
		{"unclosed placeholder", valid(KindWebhook, `{"url":"https://h","body":"{{finding.code"}`), false},
		{"unknown kind", valid("mail", `{}`), false},
		{"unknown event", func() Target {
			t := valid(KindSyslog, `{"host":"h"}`)
			t.Events = []string{"finding.exploded"}
			return t
		}(), false},
		{"no events", func() Target { t := valid(KindSyslog, `{"host":"h"}`); t.Events = nil; return t }(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.t)
			if (err == nil) != tt.ok || (err != nil && !errors.Is(err, ErrInvalidTarget)) {
				t.Errorf("validate = %v", err)
			}
			if inBody := errors.Is(err, ErrSecretInBody); inBody != (tt.name == "secret in body") {
				t.Errorf("ErrSecretInBody = %v", inBody)
			}
		})
	}
}

func TestSyslogUDPAndTCP(t *testing.T) {
	m := findingMessage(EventFindingRaised, at, findingX, "", public)
	s := newSender()
	s.hostname = "monika-host"

	var lc net.ListenConfig
	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	target := Target{Kind: KindSyslog, Config: json.RawMessage(`{"host":"127.0.0.1","port":` + itoa(port) + `,"facility":"local3"}`)}
	if err := s.send(t.Context(), target, m, nil); err != nil {
		t.Fatalf("send udp: %v", err)
	}
	buf := make([]byte, 4096)
	_ = pc.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	got := string(buf[:n])
	// local3 = 19, error = 3 → PRI 155
	if !strings.HasPrefix(got, "<155>1 2026-10-11T12:00:00Z monika-host monika - finding.raised - {\"event\":\"finding.raised\"") {
		t.Errorf("udp message = %s", got)
	}

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(bufio.NewReader(c))
		received <- string(b)
	}()
	target.Config = json.RawMessage(`{"host":"127.0.0.1","port":` + itoa(ln.Addr().(*net.TCPAddr).Port) + `,"protocol":"tcp"}`)
	if err := s.send(t.Context(), target, m, nil); err != nil {
		t.Fatalf("send tcp: %v", err)
	}
	framed := <-received
	length, rest, _ := strings.Cut(framed, " ")
	if itoa(len(rest)) != length || !strings.HasPrefix(rest, "<131>1 ") { // local0 = 16 → 131
		t.Errorf("tcp frame = %q", framed)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestWebhook(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()
	m := findingMessage(EventFindingRaised, at, findingX, "", public)
	target := Target{Kind: KindWebhook, Config: json.RawMessage(`{"url":"` + srv.URL + `/hook/{{secret.path}}","headers":{"Authorization":"Bearer {{secret.token}}"},"body":"{\"text\": {{element.path}}}"}`)}
	if err := newSender().send(t.Context(), target, m, map[string]any{"path": "abc", "token": "t0k"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotPath != "/hook/abc" || gotAuth != "Bearer t0k" || gotBody != `{"text": "X8 / SDI 1"}` {
		t.Errorf("request: path %q, auth %q, body %q", gotPath, gotAuth, gotBody)
	}

	// Fehler verraten die URL nicht, denn sie kann Werte aus dem Secret enthalten.
	target.Config = json.RawMessage(`{"url":"http://127.0.0.1:1/{{secret.path}}"}`)
	err := newSender().send(context.Background(), target, m, map[string]any{"path": "topsecret"})
	if err == nil || strings.Contains(err.Error(), "topsecret") {
		t.Errorf("error = %v", err)
	}
}
