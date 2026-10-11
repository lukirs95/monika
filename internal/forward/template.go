package forward

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Vorlagen nach den Regeln des HTTP-Treibers (docs/ARCHITECTURE.md, Abschnitt 11): Platzhalter
// {{pfad}} ohne Anführungszeichen, eingesetzt als JSON-Wert im Body, URL-kodiert in der URL, als
// Text in Headern. {{secret.<schlüssel>}} nur in URL und Headern.

type placement int

const (
	inBody placement = iota
	inURL
	inHeader
)

// placeholders liefert die Namen aller Platzhalter; eine nicht geschlossene Klammer ist ein Fehler.
func placeholders(tpl string) ([]string, error) {
	var names []string
	for rest := tpl; ; {
		i := strings.Index(rest, "{{")
		if i < 0 {
			return names, nil
		}
		j := strings.Index(rest[i:], "}}")
		if j < 0 {
			return nil, fmt.Errorf("unclosed placeholder in %q", tpl)
		}
		names = append(names, strings.TrimSpace(rest[i+2:i+j]))
		rest = rest[i+j+2:]
	}
}

// render setzt die Platzhalter ein: Werte aus dem Ereignis über Pfade (finding.severity), aus dem
// Secret über secret.<schlüssel>. Ein fehlender Wert wird null bzw. leer.
func render(tpl string, where placement, event map[string]any, secret map[string]any) (string, error) {
	var out strings.Builder
	for rest := tpl; ; {
		i := strings.Index(rest, "{{")
		if i < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		j := strings.Index(rest[i:], "}}")
		if j < 0 {
			return "", fmt.Errorf("unclosed placeholder in %q", tpl)
		}
		name := strings.TrimSpace(rest[i+2 : i+j])
		var value any
		if key, ok := strings.CutPrefix(name, "secret."); ok {
			if where == inBody {
				return "", fmt.Errorf("secret %q in a body", key)
			}
			value = secret[key]
		} else {
			value = lookup(event, name)
		}
		out.WriteString(rest[:i])
		s, err := format(value, where)
		if err != nil {
			return "", err
		}
		out.WriteString(s)
		rest = rest[i+j+2:]
	}
}

func lookup(m map[string]any, path string) any {
	var cur any = m
	for part := range strings.SplitSeq(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[part]
	}
	return cur
}

func format(v any, where placement) (string, error) {
	if where == inBody {
		b, err := json.Marshal(v)
		return string(b), err
	}
	var s string
	switch v := v.(type) {
	case nil:
	case string:
		s = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		s = string(b)
	}
	if where == inURL {
		return url.PathEscape(s), nil
	}
	return s, nil
}
