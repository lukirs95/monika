package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Anmeldung vor dem Aufruf (README, Anmeldung): Token aus Cookie, Header oder JSON der Antwort,
// im Speicher gehalten und nach einem Status aus retry_on einmal erneuert.

const maxLoginResponse = 64 << 10 // gelesene Antwort einer Anmeldung (Token im JSON)

type authCfg struct {
	Login   string            `json:"login"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Token   tokenCfg          `json:"token"`
	Apply   applyCfg          `json:"apply"`
	RetryOn []int             `json:"retry_on"`

	headers map[string]template
	body    *template
	format  template
}

type tokenCfg struct {
	From    string `json:"from"`
	Name    string `json:"name"`
	Pointer string `json:"pointer"`
}

type applyCfg struct {
	As     string `json:"as"`
	Name   string `json:"name"`
	Format string `json:"format"`
}

// compile setzt Standardwerte und prüft, was das Schema nicht ausdrücken kann.
func (a *authCfg) compile() error {
	if a.Login == "" {
		a.Login = "on_demand"
	}
	if a.Method == "" {
		a.Method = "POST"
	}
	if len(a.RetryOn) == 0 {
		a.RetryOn = []int{http.StatusUnauthorized}
	}
	if a.Apply.As != "" && a.Apply.Format == "" {
		a.Apply.Format = "{{token}}"
	}
	var errs []error
	switch {
	case (a.Token.From == "cookie" || a.Token.From == "header") && a.Token.Name == "":
		errs = append(errs, errors.New("token: name missing"))
	case a.Token.From == "json" && !strings.HasPrefix(a.Token.Pointer, "/"):
		errs = append(errs, errors.New("token: pointer must start with /"))
	}
	secretField := func(name string) bool { return strings.HasPrefix(name, "secret.") && len(name) > len("secret.") }
	a.headers = map[string]template{}
	for k, v := range a.Headers {
		t, err := parseTemplate(v, secretField)
		if err != nil {
			errs = append(errs, fmt.Errorf("header %q: %w", k, err))
		}
		a.headers[k] = t
	}
	if a.Body != "" {
		t, err := parseTemplate(a.Body, secretField)
		if err != nil {
			errs = append(errs, fmt.Errorf("body: %w", err))
		}
		a.body = &t
	}
	if a.Apply.As != "" { // ohne apply geht der Token nur über {{auth.token}} im Body mit
		var err error
		if a.format, err = parseTemplate(a.Apply.Format, func(name string) bool { return name == "token" }); err != nil {
			errs = append(errs, fmt.Errorf("apply.format: %w", err))
		} else if !strings.Contains(a.Apply.Format, "{{") {
			errs = append(errs, errors.New("apply.format: needs {{token}}"))
		}
	}
	return errors.Join(errs...)
}

// check prüft die Vorlagen gegen das Secret: Jedes Feld muss es geben, und der Body muss mit
// eingesetzten Werten JSON sein. Der Inhalt des Secrets erscheint nie in einem Fehler.
func (a *authCfg) check(values map[string]any) error {
	var errs []error
	templates := []template{}
	for _, t := range a.headers {
		templates = append(templates, t)
	}
	if a.body != nil {
		templates = append(templates, *a.body)
	}
	for _, t := range templates {
		for _, p := range t.parts {
			if _, ok := values[p.name]; p.name != "" && !ok {
				errs = append(errs, fmt.Errorf("{{%s}}: no such field in the secret", p.name))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if a.body != nil && !json.Valid([]byte(a.body.render(values, asJSON))) {
		return errors.New("body is no valid JSON with the values of the secret")
	}
	return nil
}

// secretValues sind die Felder des Secrets als Platzhalter secret.<feld> (String, Zahl, Bool).
func secretValues(raw json.RawMessage) map[string]any {
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	out := map[string]any{}
	for k, v := range fields {
		switch v.(type) {
		case string, float64, bool:
			out["secret."+k] = v
		}
	}
	return out
}

// authError ist eine gescheiterte Anmeldung mit Antwort (README: rejected mit auth: true, ohne
// Body der Anmeldung).
type authError struct {
	status int
	reason string
}

func (e *authError) Error() string { return fmt.Sprintf("login %s (HTTP %d)", e.reason, e.status) }

// authenticator hält den Token im Speicher; nur eine Anmeldung läuft zur selben Zeit.
type authenticator struct {
	cfg    *authCfg
	values map[string]any // Felder des Secrets

	mu       sync.Mutex
	token    string
	gen      uint64        // zählt die Anmeldungen; eine Anfrage merkt sich, mit welchem Token sie lief
	inflight chan struct{} // offen, solange eine Anmeldung läuft
}

// current liefert den Token und seine Generation.
func (a *authenticator) current() (string, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.token, a.gen
}

func (a *authenticator) retry(status int) bool { return slices.Contains(a.cfg.RetryOn, status) }

// apply setzt den Token in die Anfrage; ohne Token oder ohne apply nichts.
func (a *authenticator) apply(req *http.Request, token string) {
	if token == "" || a.cfg.Apply.As == "" {
		return
	}
	v := a.cfg.format.render(map[string]any{"token": token}, asText)
	if a.cfg.Apply.As == "cookie" {
		req.AddCookie(&http.Cookie{Name: a.cfg.Apply.Name, Value: v})
		return
	}
	req.Header.Set(a.cfg.Apply.Name, v)
}

// refresh holt einen neuen Token, weil eine Anfrage mit dem Token der Generation failed scheiterte.
// Hat inzwischen ein anderer Aufruf einen neueren geholt, gilt der; läuft gerade eine Anmeldung,
// wartet refresh auf sie.
func (a *authenticator) refresh(ctx context.Context, failed uint64, login func(context.Context) (string, error)) (string, error) {
	a.mu.Lock()
	for a.inflight != nil {
		ch := a.inflight
		a.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		a.mu.Lock()
	}
	if a.gen != failed {
		token := a.token
		a.mu.Unlock()
		return token, nil
	}
	ch := make(chan struct{})
	a.inflight = ch
	a.mu.Unlock()

	token, err := login(ctx)

	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.token = token
		a.gen++
	}
	a.inflight = nil
	close(ch)
	return token, err
}

// login sendet die Anmeldung und liest den Token aus der Antwort.
func (s *source) login(ctx context.Context) (string, error) {
	a := s.auth.cfg
	var body io.Reader
	if a.body != nil {
		body = strings.NewReader(a.body.render(s.auth.values, asJSON))
	}
	req, err := http.NewRequestWithContext(ctx, a.Method, s.url(a.Path, nil), body)
	if err != nil {
		return "", err
	}
	for k, v := range s.secret.Headers {
		req.Header.Set(k, v)
	}
	for k, t := range a.headers {
		req.Header.Set(k, t.render(s.auth.values, asText))
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.do(req)
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	if !ok(resp.StatusCode) {
		return "", &authError{status: resp.StatusCode, reason: "rejected"}
	}
	token, err := extractToken(resp, a.Token)
	if err != nil || token == "" {
		return "", &authError{status: resp.StatusCode, reason: "response has no token"}
	}
	return token, nil
}

// extractToken liest den Token aus Cookie, Header oder JSON der Antwort.
func extractToken(resp *http.Response, t tokenCfg) (string, error) {
	switch t.From {
	case "cookie":
		for _, c := range resp.Cookies() {
			if c.Name == t.Name {
				return c.Value, nil
			}
		}
		return "", errors.New("no such cookie")
	case "header":
		return resp.Header.Get(t.Name), nil
	}
	var doc any
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxLoginResponse)).Decode(&doc); err != nil {
		return "", err
	}
	v, err := jsonPointer(doc, t.Pointer)
	if err != nil {
		return "", err
	}
	s, isString := v.(string)
	if !isString {
		return "", errors.New("token is no string")
	}
	return s, nil
}

// jsonPointer löst einen JSON Pointer (RFC 6901) in einem dekodierten Dokument auf.
func jsonPointer(doc any, pointer string) (any, error) {
	if pointer == "" {
		return doc, nil
	}
	cur := doc
	for _, tok := range strings.Split(pointer[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[tok]
			if !ok {
				return nil, fmt.Errorf("no member %q", tok)
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(v) {
				return nil, fmt.Errorf("no element %q", tok)
			}
			cur = v[i]
		default:
			return nil, fmt.Errorf("cannot descend into %q", tok)
		}
	}
	return cur, nil
}
