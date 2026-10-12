package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Vorlagen mit Platzhaltern {{name}} (README, Aktion trigger): eingesetzt je nach Stelle als
// JSON-Wert, Pfadabschnitt oder Text.

type mode int

const (
	asJSON        mode = iota // Body: JSON-Wert
	asPathSegment             // Pfad: URL-kodierter Abschnitt
	asText                    // Query-Werte (kodiert url.Values) und Header
)

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*\}\}`)

// template ist eine geparste Vorlage: Text und Platzhalter im Wechsel.
type template struct {
	parts []part
}

type part struct {
	text string
	name string // nicht leer: Platzhalter
}

// parseTemplate zerlegt s; jeder Platzhalter muss known sein, ein offenes {{ ist ein Fehler.
func parseTemplate(s string, known func(name string) bool) (template, error) {
	var t template
	var errs []error
	rest := 0
	for _, m := range placeholder.FindAllStringSubmatchIndex(s, -1) {
		t.parts = append(t.parts, part{text: s[rest:m[0]]})
		name := s[m[2]:m[3]]
		if !known(name) {
			errs = append(errs, fmt.Errorf("unknown placeholder {{%s}}", name))
		}
		t.parts = append(t.parts, part{name: name})
		rest = m[1]
	}
	t.parts = append(t.parts, part{text: s[rest:]})
	for _, p := range t.parts {
		if strings.Contains(p.text, "{{") {
			errs = append(errs, errors.New("unclosed placeholder"))
		}
	}
	return t, errors.Join(errs...)
}

// render setzt die Werte ein.
func (t template) render(values map[string]any, m mode) string {
	var b strings.Builder
	for _, p := range t.parts {
		if p.name == "" {
			b.WriteString(p.text)
			continue
		}
		v := values[p.name]
		switch m {
		case asJSON:
			j, _ := json.Marshal(v) // nur bool, int64, float64, string
			b.Write(j)
		case asPathSegment:
			b.WriteString(url.PathEscape(text(v)))
		case asText:
			b.WriteString(text(v))
		}
	}
	return b.String()
}

// text ist ein Wert als Text, für Pfad, Query und Header.
func text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return fmt.Sprint(v)
}
