package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"slices"
	"sort"

	"github.com/lukirs95/monika/sdk"
)

// param ist ein geprüfter Parameter eines Aufrufs (README, Aktion trigger).
type param struct {
	sdk.Param
	hasDefault bool
}

func compileParam(pc paramConfig) (param, error) {
	p := param{Param: sdk.Param{Setting: sdk.Setting{Type: pc.Type, Min: pc.Min, Max: pc.Max, Values: pc.Values}}}
	if pc.Type == sdk.SettingEnum && len(pc.Values) == 0 {
		return param{}, errors.New("enum needs values")
	}
	if len(pc.Default) > 0 {
		v, err := p.decode(pc.Default)
		if err != nil {
			return param{}, fmt.Errorf("default: %w", err)
		}
		p.Default, p.hasDefault = v, true
	}
	return p, nil
}

// decode liest einen JSON-Wert in den Go-Typ des Parameters und prüft Grenzen und Werte.
func (p param) decode(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	switch p.Type {
	case sdk.SettingInt, sdk.SettingFloat:
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Errorf("want a number, got %s", raw)
		}
		f, err := n.Float64()
		if err != nil {
			return nil, err
		}
		if (p.Min != nil && f < *p.Min) || (p.Max != nil && f > *p.Max) {
			return nil, fmt.Errorf("%s is out of range", n)
		}
		if p.Type == sdk.SettingFloat {
			return f, nil
		}
		i, err := n.Int64()
		if err != nil || math.Trunc(f) != f {
			return nil, fmt.Errorf("want an integer, got %s", n)
		}
		return i, nil
	case sdk.SettingBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("want true or false, got %s", raw)
		}
		return b, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("want a string, got %s", raw)
	}
	switch p.Type {
	case sdk.SettingEnum:
		if !slices.Contains(p.Values, s) {
			return nil, fmt.Errorf("%q is not one of %v", s, p.Values)
		}
	case sdk.SettingIPv4:
		if a, err := netip.ParseAddr(s); err != nil || !a.Is4() {
			return nil, fmt.Errorf("%q is no IPv4 address", s)
		}
	}
	return s, nil
}

// sample ist ein Wert zum Prüfen der Vorlagen beim Laden: der Standardwert, sonst ein gültiger.
func (p param) sample() any {
	if p.hasDefault {
		return p.Default
	}
	switch p.Type {
	case sdk.SettingInt:
		if p.Min != nil {
			return int64(math.Ceil(*p.Min))
		}
		return int64(0)
	case sdk.SettingFloat:
		if p.Min != nil {
			return *p.Min
		}
		return 0.0
	case sdk.SettingBool:
		return false
	case sdk.SettingEnum:
		return p.Values[0]
	case sdk.SettingIPv4:
		return "192.0.2.1"
	}
	return "x"
}

// values prüft die Parameter eines trigger gegen das Schema und ergänzt Standardwerte. Abgelehnte
// Felder kommen sortiert zurück, mit einer Beschreibung.
func (c *call) values(raw json.RawMessage) (map[string]any, []string, string) {
	given := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &given); err != nil {
			return nil, []string{"params"}, "params must be a JSON object"
		}
	}
	out := map[string]any{}
	var rejected, details []string
	for name := range given {
		if _, ok := c.params[name]; !ok {
			rejected = append(rejected, name)
			details = append(details, name+": unknown parameter")
		}
	}
	for name, p := range c.params {
		raw, ok := given[name]
		switch {
		case ok:
			v, err := p.decode(raw)
			if err != nil {
				rejected = append(rejected, name)
				details = append(details, name+": "+err.Error())
				continue
			}
			out[name] = v
		case p.hasDefault:
			out[name] = p.Default
		default:
			rejected = append(rejected, name)
			details = append(details, name+": missing")
		}
	}
	sort.Strings(rejected)
	sort.Strings(details)
	if len(rejected) > 0 {
		return nil, rejected, fmt.Sprint(details)
	}
	return out, nil, ""
}
