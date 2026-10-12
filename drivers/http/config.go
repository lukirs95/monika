package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// configSchema ist das JSON-Schema für device_source.config (README, Konfiguration). Was es nicht
// ausdrücken kann (eindeutige ids, bekannte Platzhalter, gültige Vorlagen), prüft parseConfig.
const configSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["calls"],
  "properties": {
    "timeout_s": {"type": "number", "minimum": 1, "maximum": 30},
    "insecure_skip_verify": {"type": "boolean"},
    "health": {
      "type": "object",
      "additionalProperties": false,
      "required": ["path", "interval_s"],
      "properties": {
        "method": {"enum": ["GET", "HEAD", "POST"]},
        "path": {"type": "string", "pattern": "^/[^?]*$"},
        "interval_s": {"type": "integer", "minimum": 1, "maximum": 3600}
      }
    },
    "calls": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "name", "method", "path"],
        "properties": {
          "id": {"type": "string", "pattern": "^[a-z0-9][a-z0-9._-]{0,63}$"},
          "name": {"type": "string", "minLength": 1},
          "method": {"enum": ["GET", "POST", "PUT", "PATCH", "DELETE"]},
          "path": {"type": "string", "pattern": "^/[^?]*$"},
          "query": {"type": "object", "additionalProperties": {"type": "string"}},
          "headers": {"type": "object", "additionalProperties": {"type": "string"}},
          "body": {"type": "string"},
          "critical": {"type": "boolean"},
          "params": {
            "type": "object",
            "additionalProperties": {
              "type": "object",
              "additionalProperties": false,
              "required": ["type"],
              "properties": {
                "type": {"enum": ["int", "float", "bool", "string", "enum", "ipv4"]},
                "min": {"type": "number"},
                "max": {"type": "number"},
                "values": {"type": "array", "items": {"type": "string"}, "minItems": 1},
                "default": {"type": ["boolean", "number", "string"]}
              }
            }
          }
        }
      }
    }
  }
}`

const (
	defaultTimeout  = 10 * time.Second
	maxResponseBody = 4 << 10 // Body eines abgelehnten Aufrufs in der Antwort des Laufs
)

// config ist device_source.config.
type config struct {
	TimeoutS           float64      `json:"timeout_s"`
	InsecureSkipVerify bool         `json:"insecure_skip_verify"`
	Health             *healthCfg   `json:"health"`
	Calls              []callConfig `json:"calls"`
}

type healthCfg struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	IntervalS int    `json:"interval_s"`
}

type callConfig struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Method   string                 `json:"method"`
	Path     string                 `json:"path"`
	Query    map[string]string      `json:"query"`
	Headers  map[string]string      `json:"headers"`
	Body     string                 `json:"body"`
	Critical bool                   `json:"critical"`
	Params   map[string]paramConfig `json:"params"`
}

type paramConfig struct {
	Type    string          `json:"type"`
	Min     *float64        `json:"min"`
	Max     *float64        `json:"max"`
	Values  []string        `json:"values"`
	Default json.RawMessage `json:"default"`
}

// call ist ein geprüfter Aufruf mit kompilierten Vorlagen.
type call struct {
	callConfig
	params map[string]param
	path   template
	query  map[string]template
	body   *template // nil = ohne Body
}

// parseConfig liest und prüft die Konfiguration über das Schema hinaus. Fehler sammelt es alle.
func parseConfig(raw json.RawMessage) (config, []*call, error) {
	var c config
	if err := json.Unmarshal(raw, &c); err != nil {
		return config{}, nil, fmt.Errorf("config: %w", err)
	}
	var errs []error
	if c.TimeoutS == 0 {
		c.TimeoutS = defaultTimeout.Seconds()
	}
	if h := c.Health; h != nil && h.Method == "" {
		h.Method = "GET"
	}
	seen := map[string]bool{}
	calls := make([]*call, 0, len(c.Calls))
	for _, cc := range c.Calls {
		if seen[cc.ID] {
			errs = append(errs, fmt.Errorf("call %q: id used twice", cc.ID))
			continue
		}
		seen[cc.ID] = true
		cl, err := compileCall(cc)
		if err != nil {
			errs = append(errs, fmt.Errorf("call %q: %w", cc.ID, err))
			continue
		}
		calls = append(calls, cl)
	}
	return c, calls, errors.Join(errs...)
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// compileCall prüft Parameter und Vorlagen eines Aufrufs: nur bekannte Platzhalter, und mit den
// Standardwerten eingesetzt ein gültiges JSON bzw. ein gültiger Pfad.
func compileCall(cc callConfig) (*call, error) {
	if !idPattern.MatchString(cc.ID) || strings.Contains(cc.Path, "?") || !strings.HasPrefix(cc.Path, "/") {
		return nil, errors.New("needs an id of a–z, 0–9, '.', '_', '-' and a path starting with / without ?")
	}
	cl := &call{callConfig: cc, params: map[string]param{}, query: map[string]template{}}
	var errs []error
	for name, pc := range cc.Params {
		p, err := compileParam(pc)
		if err != nil {
			errs = append(errs, fmt.Errorf("param %q: %w", name, err))
			continue
		}
		cl.params[name] = p
	}
	known := func(name string) bool { _, ok := cl.params[name]; return ok }
	var err error
	if cl.path, err = parseTemplate(cc.Path, known); err != nil {
		errs = append(errs, fmt.Errorf("path: %w", err))
	}
	for k, v := range cc.Query {
		t, err := parseTemplate(v, known)
		if err != nil {
			errs = append(errs, fmt.Errorf("query %q: %w", k, err))
		}
		cl.query[k] = t
	}
	if cc.Body != "" {
		t, err := parseTemplate(cc.Body, known)
		if err != nil {
			errs = append(errs, fmt.Errorf("body: %w", err))
		}
		cl.body = &t
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	// Probe mit Standardwerten bzw. Beispielwerten: Ergibt die Vorlage Unsinn, merkt es der User
	// beim Speichern und nicht erst beim ersten Aufruf.
	sample := map[string]any{}
	for name, p := range cl.params {
		sample[name] = p.sample()
	}
	if cl.body != nil {
		if b := cl.body.render(sample, asJSON); !json.Valid([]byte(b)) {
			return nil, fmt.Errorf("body is no valid JSON with sample values: %s", b)
		}
	}
	if _, err := url.Parse("http://x" + cl.path.render(sample, asPathSegment)); err != nil {
		return nil, fmt.Errorf("path: %w", err)
	}
	return cl, nil
}
