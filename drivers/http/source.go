package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// source ist eine laufende Quelle: ein Ziel mit seinen Aufrufen.
type source struct {
	log        *slog.Logger
	sched      sdk.Scheduler
	base       *url.URL
	client     *http.Client
	timeout    time.Duration
	health     *healthCfg
	secret     secretValue
	auth       *authenticator // nil ohne auth
	retryLogin time.Duration  // login on_start: Abstand neuer Versuche
	calls      map[string]*call
	order      []*call

	observed chan observation // Fakten aus Execute an die Goroutine von Run
	checked  chan error       // Ergebnis des Health-Checks an die Goroutine von Run
}

// observation sind die Fakten eines Aufrufs.
type observation struct {
	id    string
	facts sdk.Facts
}

// secretValue ist das JSON des Secrets (README, Secret).
type secretValue struct {
	Username string            `json:"username"`
	Password string            `json:"password"`
	Headers  map[string]string `json:"headers"`
}

func newSource(cfg sdk.SourceConfig) (sdk.Source, error) {
	c, calls, err := parseConfig(cfg.Config)
	if err != nil {
		return nil, err
	}
	base, err := parseBase(cfg.Address)
	if err != nil {
		return nil, err
	}
	var sec secretValue
	if len(cfg.Secret) > 0 {
		if err := json.Unmarshal(cfg.Secret, &sec); err != nil {
			return nil, errors.New("secret: want a JSON object with username, password and headers") // ohne den Inhalt
		}
	}
	var auth *authenticator
	if c.Auth != nil {
		auth = &authenticator{cfg: c.Auth, values: secretValues(cfg.Secret)}
		if err := c.Auth.check(auth.values); err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // vom User gewählt, für selbst signierte Zertifikate
	}
	s := &source{
		log: cfg.Logger, sched: cfg.Scheduler, base: base, timeout: time.Duration(c.TimeoutS * float64(time.Second)),
		health: c.Health, secret: sec, auth: auth, retryLogin: loginRetry, calls: map[string]*call{}, order: calls,
		client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // Weiterleitungen nicht verfolgen
		}},
		observed: make(chan observation, 256), checked: make(chan error, 1),
	}
	for _, cl := range calls {
		s.calls[cl.ID] = cl
	}
	return s, nil
}

// parseBase prüft die Basis-URL (SourceConfig.Address).
func parseBase(address string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSuffix(address, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("address %q: want a base URL like http://host:port", address)
	}
	return u, nil
}

// Heartbeat ist der Abstand des Health-Checks; ohne ihn 0 (keine Überwachung).
func (s *source) Heartbeat() time.Duration {
	if s.health == nil {
		return 0
	}
	return time.Duration(s.health.IntervalS) * time.Second
}

// Run meldet den Stand und die Fakten der Aufrufe. Ohne Health-Check ist die Quelle sofort
// verbunden; mit ihm verbindet die erste 2xx-Antwort, ein Fehlschlag trennt.
func (s *source) Run(ctx context.Context, r sdk.Reporter) error {
	facts := map[string]sdk.Facts{} // letzter Stand pro Aufruf, für jeden Snapshot
	connected := false
	connect := func() {
		r.Connected()
		r.Snapshot(s.snapshot(facts))
		connected = true
	}
	if s.auth != nil && s.auth.cfg.Login == "on_start" && !s.loginAtStart(ctx, r) {
		return nil
	}
	if s.health == nil {
		connect()
	} else {
		s.sched.Every(ctx, s.Heartbeat(), s.checkHealth)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-s.checked:
			switch {
			case err == nil && !connected:
				connect()
			case err == nil:
				r.Heartbeat()
			case connected:
				s.log.Warn("health check failed", "err", err)
				r.Disconnected(err)
				connected = false
			}
		case o := <-s.observed:
			facts[o.id] = o.facts
			if connected {
				r.Update(sdk.Update{Facts: map[string]sdk.Facts{o.id: o.facts}})
			}
		}
	}
}

// loginAtStart meldet sich vor dem ersten Aufruf an (login: on_start). Scheitert es, meldet es
// einmal Disconnected und versucht es im Takt des Health-Checks bzw. alle 10 s erneut; false,
// wenn ctx vorher endet.
func (s *source) loginAtStart(ctx context.Context, r sdk.Reporter) bool {
	every := s.retryLogin
	if s.health != nil {
		every = s.Heartbeat()
	}
	reported := false
	for {
		_, gen := s.auth.current()
		lctx, cancel := context.WithTimeout(ctx, s.timeout)
		_, err := s.auth.refresh(lctx, gen, s.login)
		cancel()
		if err == nil {
			return true
		}
		if !reported {
			s.log.Warn("login failed", "err", err)
			r.Disconnected(err)
			reported = true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(every):
		}
	}
}

func (s *source) snapshot(facts map[string]sdk.Facts) sdk.Snapshot {
	snap := sdk.Snapshot{Facts: map[string]sdk.Facts{}}
	for _, c := range s.order {
		snap.Elements = append(snap.Elements, c.element())
		if f, ok := facts[c.ID]; ok {
			snap.Facts[c.ID] = f
		}
	}
	return snap
}

func (c *call) element() sdk.Element {
	attrs, _ := json.Marshal(map[string]string{"method": c.Method, "path": c.Path})
	e := sdk.Element{
		ExternalID: c.ID, Kind: vocab.ElementTrigger, Direction: vocab.DirectionOut, Name: c.Name,
		Attributes: attrs, Actions: []string{vocab.ActionTrigger},
	}
	if c.Critical {
		e.CriticalActions = []string{vocab.ActionTrigger}
	}
	if len(c.params) > 0 {
		ps := map[string]sdk.Param{}
		for name, p := range c.params {
			ps[name] = p.Param
		}
		e.Params = map[string]map[string]sdk.Param{vocab.ActionTrigger: ps}
	}
	return e
}

// checkHealth ruft den Health-Check auf; das Ergebnis geht an Run.
func (s *source) checkHealth(ctx context.Context) error {
	res, err := s.send(ctx, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, s.health.Method, s.url(s.health.Path, nil), nil)
	})
	if err == nil && !ok(res.status) {
		err = fmt.Errorf("health check: HTTP %d", res.status)
	}
	select {
	case s.checked <- err:
	case <-ctx.Done():
	}
	return err
}

// Execute führt trigger aus: Parameter prüfen, Anfrage senden, Fakten melden.
func (s *source) Execute(ctx context.Context, cmd sdk.Command) (sdk.Result, error) {
	c, found := s.calls[cmd.ExternalID]
	switch {
	case !found:
		return sdk.Result{}, fmt.Errorf("unknown call %q", cmd.ExternalID)
	case cmd.Action != vocab.ActionTrigger:
		return sdk.Result{}, fmt.Errorf("action %q is not supported", cmd.Action)
	}
	values, rejected, detail := c.values(cmd.Params)
	if rejected != nil {
		return sdk.Result{Status: sdk.Rejected, Detail: detail, RejectedFields: rejected}, nil
	}
	start := time.Now()
	res, err := s.send(ctx, func(ctx context.Context) (*http.Request, error) { return s.request(ctx, c, values) })
	s.observe(c.ID, start, time.Since(start), res.status)
	var authErr *authError
	switch {
	case errors.As(err, &authErr):
		resp, _ := json.Marshal(map[string]any{"status": authErr.status, "auth": true}) // ohne Body der Anmeldung
		return sdk.Result{Status: sdk.Rejected, Detail: authErr.Error(), Response: resp}, nil
	case err != nil:
		return sdk.Result{}, err
	case ok(res.status):
		resp, _ := json.Marshal(map[string]int{"status": res.status})
		return sdk.Result{Status: sdk.Accepted, Response: resp}, nil
	}
	resp, _ := json.Marshal(map[string]any{"status": res.status, "body": res.body})
	return sdk.Result{Status: sdk.Rejected, Detail: fmt.Sprintf("HTTP %d", res.status), Response: resp}, nil
}

// observe gibt die Fakten eines Aufrufs an Run; läuft Run nicht oder hängt, gehen sie verloren.
func (s *source) observe(id string, start time.Time, d time.Duration, status int) {
	o := observation{id: id, facts: sdk.Facts{
		"call.ok":          ok(status),
		"call.called_at":   start.UTC().Format(time.RFC3339Nano),
		"call.duration_ms": d.Milliseconds(),
		factStatusCode:     int64(status),
	}}
	select {
	case s.observed <- o:
	default:
		s.log.Warn("facts of a call dropped", "call", id)
	}
}

// request baut die Anfrage eines Aufrufs mit eingesetzten Werten.
func (s *source) request(ctx context.Context, c *call, values map[string]any) (*http.Request, error) {
	q := url.Values{}
	for k, t := range c.query {
		q.Set(k, t.render(values, asText))
	}
	var body io.Reader
	if c.body != nil {
		body = strings.NewReader(c.body.render(values, asJSON))
	}
	req, err := http.NewRequestWithContext(ctx, c.Method, s.url(c.path.render(values, asPathSegment), q), body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// url hängt den (schon kodierten) Pfad und die Query an die Basis-URL.
func (s *source) url(path string, q url.Values) string {
	u := s.base.String() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// response ist eine gelesene Antwort: Status und der Anfang des Bodys.
type response struct {
	status int
	body   string
}

// send sendet eine Anfrage mit den Zugangsdaten des Secrets bzw. dem Token der Anmeldung. build
// baut sie, für eine Wiederholung erneut. Antwortet das Ziel mit einem Status aus retry_on, meldet
// sich send an und wiederholt genau einmal; scheitert die Anmeldung, kommt die erste Antwort mit
// dem Fehler zurück. Ein anderer Fehler heißt: keine Antwort.
func (s *source) send(ctx context.Context, build func(context.Context) (*http.Request, error)) (response, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var token string
	var gen uint64
	if s.auth != nil {
		token, gen = s.auth.current()
	}
	res, err := s.sendOnce(ctx, build, token)
	if err != nil || s.auth == nil || !s.auth.retry(res.status) {
		return res, err
	}
	token, err = s.auth.refresh(ctx, gen, s.login)
	if err != nil {
		return res, err
	}
	return s.sendOnce(ctx, build, token)
}

func (s *source) sendOnce(ctx context.Context, build func(context.Context) (*http.Request, error), token string) (response, error) {
	req, err := build(ctx)
	if err != nil {
		return response{}, err
	}
	if s.auth == nil && (s.secret.Username != "" || s.secret.Password != "") {
		req.SetBasicAuth(s.secret.Username, s.secret.Password)
	}
	for k, v := range s.secret.Headers {
		req.Header.Set(k, v)
	}
	if s.auth != nil {
		s.auth.apply(req, token)
	}
	resp, err := s.do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // Rest verwerfen, Verbindung wiederverwenden
	return response{status: resp.StatusCode, body: string(bytes.ToValidUTF8(b, []byte("�")))}, nil
}

// do sendet eine fertige Anfrage. Fehler nennen die URL ohne Passwort; *url.Error nennt sie
// sonst ganz, falls eins in der Adresse steht.
func (s *source) do(req *http.Request) (*http.Response, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL.Redacted(), err)
	}
	return resp, nil
}

func ok(status int) bool { return status >= 200 && status < 300 }
