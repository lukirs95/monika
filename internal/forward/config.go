package forward

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/evaluation"
)

// Fehler beim Prüfen eines Ziels; ErrSecretInBody steht zusätzlich in ErrInvalidTarget.
var (
	// ErrInvalidTarget bedeutet, dass ein Ziel unvollständig oder falsch konfiguriert ist.
	ErrInvalidTarget = errors.New("invalid forward target")
	// ErrSecretInBody bedeutet, dass die Vorlage des Bodys ein Secret einsetzen würde; Secrets
	// gehören nur in URL und Header.
	ErrSecretInBody = errors.New("secret placeholder only in url and headers, not in the body")
)

var events = []string{EventFindingRaised, EventFindingCleared, EventFindingWithdrawn, EventFindingAcked, EventActionFinished}

// syslogConfig ist Target.Config bei syslog.
type syslogConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`     // Standard 514, bei tls 6514
	Protocol string `json:"protocol,omitempty"` // udp (Standard), tcp, tls
	Facility string `json:"facility,omitempty"` // Standard local0
}

// webhookConfig ist Target.Config bei webhook.
type webhookConfig struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"` // Standard POST
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"` // Vorlage; leer = das JSON des Ereignisses
}

var facilities = map[string]int{
	"kern": 0, "user": 1, "mail": 2, "daemon": 3, "auth": 4, "syslog": 5, "lpr": 6, "news": 7,
	"uucp": 8, "cron": 9, "authpriv": 10, "ftp": 11,
	"local0": 16, "local1": 17, "local2": 18, "local3": 19, "local4": 20, "local5": 21, "local6": 22, "local7": 23,
}

// validate prüft ein Ziel vor dem Speichern.
func validate(t Target) error {
	var errs []error
	if t.ID == uuid.Nil || t.Name == "" {
		errs = append(errs, errors.New("needs an ID and a name"))
	}
	if len(t.Events) == 0 {
		errs = append(errs, errors.New("needs at least one event"))
	}
	for _, e := range t.Events {
		if !slices.Contains(events, e) {
			errs = append(errs, fmt.Errorf("unknown event %q", e))
		}
	}
	switch t.MinSeverity {
	case "", evaluation.SeverityInfo, evaluation.SeverityWarning, evaluation.SeverityError:
	default:
		errs = append(errs, fmt.Errorf("unknown severity %q", t.MinSeverity))
	}
	switch t.Kind {
	case KindSyslog:
		_, err := parseSyslog(t.Config)
		errs = append(errs, err)
	case KindWebhook:
		_, err := parseWebhook(t.Config)
		errs = append(errs, err)
	default:
		errs = append(errs, fmt.Errorf("unknown kind %q", t.Kind))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w %q: %w", ErrInvalidTarget, t.Name, err)
	}
	return nil
}

func parseSyslog(raw json.RawMessage) (syslogConfig, error) {
	var c syslogConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("syslog config: %w", err)
	}
	if c.Protocol == "" {
		c.Protocol = "udp"
	}
	if c.Facility == "" {
		c.Facility = "local0"
	}
	if c.Port == 0 {
		c.Port = 514
		if c.Protocol == "tls" {
			c.Port = 6514
		}
	}
	var errs []error
	if c.Host == "" {
		errs = append(errs, errors.New("syslog needs a host"))
	}
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("invalid port %d", c.Port))
	}
	if !slices.Contains([]string{"udp", "tcp", "tls"}, c.Protocol) {
		errs = append(errs, fmt.Errorf("unknown protocol %q", c.Protocol))
	}
	if _, ok := facilities[c.Facility]; !ok {
		errs = append(errs, fmt.Errorf("unknown facility %q", c.Facility))
	}
	return c, errors.Join(errs...)
}

func parseWebhook(raw json.RawMessage) (webhookConfig, error) {
	var c webhookConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("webhook config: %w", err)
	}
	if c.Method == "" {
		c.Method = "POST"
	}
	var errs []error
	if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH"}, strings.ToUpper(c.Method)) {
		errs = append(errs, fmt.Errorf("unknown method %q", c.Method))
	}
	// Die URL muss auch mit eingesetzten Platzhaltern eine sein; geprüft mit leeren Werten.
	if u, err := render(c.URL, inURL, nil, nil); err != nil {
		errs = append(errs, err)
	} else if p, err := url.Parse(u); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		errs = append(errs, fmt.Errorf("webhook needs an http(s) url, got %q", c.URL))
	}
	for k, v := range c.Headers {
		if _, err := placeholders(k + v); err != nil {
			errs = append(errs, err)
		}
	}
	names, err := placeholders(c.Body)
	errs = append(errs, err)
	for _, n := range names {
		if strings.HasPrefix(n, "secret.") {
			errs = append(errs, fmt.Errorf("{{%s}}: %w", n, ErrSecretInBody))
		}
	}
	return c, errors.Join(errs...)
}
