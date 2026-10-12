package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/coretest"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// sse ist ein Ereignis aus dem Strom.
type sse struct {
	id, event string
	data      json.RawMessage
}

// sseClient liest einen Strom über echtes HTTP.
type sseClient struct {
	t      *testing.T
	events chan sse
	pings  chan struct{}
	cancel func()
}

func openStream(t *testing.T, base, cookie, lastEventID string) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/events", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: cookie})
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET /events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		cancel()
		t.Fatalf("GET /events = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	c := &sseClient{t: t, events: make(chan sse, 256), pings: make(chan struct{}, 256), cancel: cancel}
	go func() {
		defer close(c.events)
		r := bufio.NewReader(resp.Body)
		var ev sse
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSuffix(line, "\n")
			switch {
			case line == "":
				if ev.event != "" {
					c.events <- ev
				}
				ev = sse{}
			case strings.HasPrefix(line, ": ping"):
				select {
				case c.pings <- struct{}{}:
				default:
				}
			case strings.HasPrefix(line, "id: "):
				ev.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.event = line[7:]
			case strings.HasPrefix(line, "data: "):
				ev.data = json.RawMessage(line[6:])
			}
		}
	}()
	t.Cleanup(cancel)
	return c
}

// wait liest Ereignisse, bis eines vom Typ event die Bedingung erfüllt.
func (c *sseClient) wait(event string, ok func(json.RawMessage) bool) sse {
	c.t.Helper()
	read := c.until(event, ok)
	return read[len(read)-1]
}

// until liest Ereignisse, bis eines vom Typ event die Bedingung erfüllt, und liefert alle.
func (c *sseClient) until(event string, ok func(json.RawMessage) bool) []sse {
	c.t.Helper()
	var read []sse
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, open := <-c.events:
			if !open {
				c.t.Fatalf("stream ended while waiting for %s", event)
			}
			read = append(read, ev)
			if ev.event == event && (ok == nil || ok(ev.data)) {
				return read
			}
		case <-timeout:
			c.t.Fatalf("timeout waiting for %s", event)
		}
	}
}

func contains(s string) func(json.RawMessage) bool {
	return func(d json.RawMessage) bool { return strings.Contains(string(d), s) }
}

// streamWorld ist die Szene in einem laufenden Core mit Verteiler und echtem HTTP-Server.
func streamWorld(t *testing.T) (*scene, *Server, *coretest.Running, string) {
	t.Helper()
	sc, _ := newScene(t)
	running := coretest.Start(t, coretest.Options{
		Snapshot: sc.snapshot(),
		Findings: []evaluation.Finding{{ID: sc.findingP1, ElementID: sc.p1, Origin: evaluation.OriginDevice, Code: "los", Severity: evaluation.SeverityError, RaisedAt: time.Now()}},
	})
	s := newServerWith(t, running)
	s.times = streamTimes{keepalive: 50 * time.Millisecond, recheck: 50 * time.Millisecond}
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{
		anna.UserID: {{Permission: auth.PermView, ScopeElementID: sc.g}, {Permission: auth.PermAcknowledge, ScopeElementID: sc.g}},
		boss.UserID: {{Permission: auth.PermView}, {Permission: auth.PermInventoryEdit}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- s.Run(ctx) }()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		cancel()
		<-done
		srv.Close()
	})
	waitFor(t, "distributor", func() bool { _, _, err := s.dist.open(anna, ""); return err == nil })
	return sc, s, running, srv.URL
}

func TestEventStream(t *testing.T) {
	sc, s, running, base := streamWorld(t)
	c := openStream(t, base, "anna", "")
	hello := c.wait("hello", nil)
	var h struct {
		StreamID uuid.UUID `json:"stream_id"`
		Version  uint64
	}
	if err := json.Unmarshal(hello.data, &h); err != nil || h.StreamID == uuid.Nil {
		t.Fatalf("hello = %s", hello.data)
	}

	// Sichtbares Element geändert; P2 wird durch die Mitgliedschaft sichtbar (V4) und wieder unsichtbar.
	if hello.id != "" {
		t.Errorf("hello with id %s", hello.id)
	}
	run(t, s, []step{{"label", "PUT", "/api/v1/elements/" + sc.p1.String() + "/user-fields", `{"label":"Cam 1","notes":null,"monitored":true}`, "boss", 200, ""}})
	labelled := c.wait("elements_changed", contains(`"label":"Cam 1"`))
	b := run(t, s, []step{{"member", "POST", "/api/v1/groups/" + sc.g.String() + "/members", `{"element_id":"` + sc.p2.String() + `"}`, "boss", 201, ""}})
	link := idOf(t, b["member"])
	c.wait("elements_changed", func(d json.RawMessage) bool {
		return contains(sc.p2.String())(d) && contains(`"links":[{`)(d) && contains(link)(d)
	})
	run(t, s, []step{{"unmember", "DELETE", "/api/v1/memberships/" + link, "", "boss", 204, ""}})
	c.wait("elements_changed", contains(`"removed_elements":["`+sc.p2.String()+`"]`))

	// Quittieren, Health und Fakten nur für Beobachtetes.
	run(t, s, []step{{"ack", "POST", "/api/v1/findings/" + sc.findingP1.String() + "/ack", "", "anna", 204, ""}})
	c.wait("finding_acked", contains(`"acked_by_id":"`+anna.UserID.String()+`"`))

	w := call(t, s, "POST", "/api/v1/events/"+h.StreamID.String()+"/watch", `{"add":["`+sc.p1.String()+`"]}`, "anna")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"watched":1`) {
		t.Fatalf("watch = %d %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/api/v1/events/"+h.StreamID.String()+"/watch", `{"add":["`+sc.p1.String()+`"]}`, "boss"); w.Code != 404 {
		t.Errorf("watch on another user's stream = %d", w.Code)
	}
	if w := call(t, s, "POST", "/api/v1/events/"+h.StreamID.String()+"/watch", `{"add":["`+sc.q1.String()+`"]}`, "anna"); w.Code != 404 {
		t.Errorf("watch an invisible element = %d", w.Code)
	}
	rep := running.Reporter(sc.s8)
	running.Post(core.SourceStarted{SourceID: sc.s8})
	rep.Connected()
	rep.Snapshot(sdk.Snapshot{
		Elements: []sdk.Element{{ExternalID: "sdi/1", Kind: vocab.ElementPort, Name: "SDI 1"}, {ExternalID: "sdi/2", Kind: vocab.ElementPort, Name: "SDI 2"}},
		Facts:    map[string]sdk.Facts{"sdi/1": {"system.temperature_c": 40.0}, "sdi/2": {"system.temperature_c": 41.0}},
	})
	var health, facts bool
	for !health || !facts {
		select {
		case ev := <-c.events:
			health = health || ev.event == "health_changed" && contains(sc.p1.String())(ev.data)
			facts = facts || ev.event == "facts_changed" && contains(`"system.temperature_c":40`)(ev.data)
		case <-time.After(5 * time.Second):
			t.Fatalf("source live: health %v, facts %v", health, facts)
		}
	}
	rep.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/2": {"system.temperature_c": 60.0}}})
	rep.Update(sdk.Update{Facts: map[string]sdk.Facts{"sdi/1": {"system.temperature_c": 45.0}}})
	got := c.wait("facts_changed", contains(`"system.temperature_c":45`))
	if strings.Contains(string(got.data), sc.p2.String()) {
		t.Errorf("facts of an unwatched element: %s", got.data)
	}
	select {
	case <-c.pings:
	case <-time.After(time.Second):
		t.Error("no keepalive")
	}

	// Wiederaufnahme: neuer Strom ab einer älteren Version bekommt das Verpasste ohne Fakten.
	c.cancel()
	resumed := openStream(t, base, "anna", labelled.id)
	resumed.wait("hello", nil)
	// Quittiert und danach beendet (die Quelle meldete den Alarm nicht mehr): zusammengefasst
	// kommt nur der Endstand; Fakten liegen nicht im Ring.
	for _, ev := range resumed.until("finding_ended", contains(sc.findingP1.String())) {
		if ev.event == "facts_changed" || ev.event == "finding_acked" {
			t.Errorf("resumed with %s", ev.event)
		}
	}

	// Die Sitzung endet: Der Strom schließt sich bei der nächsten Prüfung.
	s.cfg.Auth.(*fakeAuth).mu.Lock()
	s.cfg.Auth.(*fakeAuth).ended = append(s.cfg.Auth.(*fakeAuth).ended, "anna")
	s.cfg.Auth.(*fakeAuth).mu.Unlock()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case _, open := <-resumed.events:
			if !open {
				if n := s.dist.Streams(); n > 1 { // der Strom aus streamWorld bleibt
					t.Errorf("open streams = %d", n)
				}
				return
			}
		case <-timeout:
			t.Fatal("stream not ended after the session ended")
		}
	}
}

func TestEventStreamGauge(t *testing.T) {
	_, s, _, base := streamWorld(t)
	c := openStream(t, base, "boss", "")
	c.wait("hello", nil)
	if g := s.Gauges()["api_streams"]; g < 2 {
		t.Errorf("api_streams = %d", g)
	}
	c.cancel()
	waitFor(t, "stream closed", func() bool { return s.Gauges()["api_streams"] == 1 })
}
