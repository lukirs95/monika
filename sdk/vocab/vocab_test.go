package vocab

import (
	"regexp"
	"strings"
	"testing"
)

var snake = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

// unitTypes ist die Typregel nach Endung aus docs/contracts/vocabulary.md, längere Endungen zuerst.
var unitTypes = []struct {
	suffix string
	typ    Type
}{
	{"_percent", Float}, {"_mbps", Float}, {"_dbm", Float},
	{"_rpm", Int}, {"_ns", Int}, {"_ms", Int}, {"_hz", Int}, {"_ma", Float}, {"_mv", Float}, {"_ui", Float},
	{"_s", Int}, {"_c", Float}, {"_v", Float}, {"_w", Float}, {"_m", Float},
}

func TestFields(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Fields() {
		key := f.Key()
		switch {
		case seen[key]:
			t.Errorf("%s: duplicate", key)
		case !snake.MatchString(f.Measurement) || !snake.MatchString(f.Name):
			t.Errorf("%s: measurement and field must be snake_case", key)
		case f.Name == f.Measurement:
			t.Errorf("%s: field must not be named like its measurement", key)
		case !f.Type.Valid() || !f.Kind.Valid():
			t.Errorf("%s: invalid type %q or kind %q", key, f.Type, f.Kind)
		case f.Kind == Counter && f.Type != Int:
			t.Errorf("%s: counter must be int", key)
		case f.Measurement == MeasurementHealth || f.Measurement == MeasurementRollup || f.Measurement == MeasurementMonika:
			t.Errorf("%s: measurement is reserved", key)
		case strings.HasSuffix(f.Name, RateSuffix):
			t.Errorf("%s: rates are derived from counters, not listed", key)
		}
		seen[key] = true
		for _, u := range unitTypes {
			if strings.HasSuffix(f.Name, u.suffix) {
				if f.Type != u.typ {
					t.Errorf("%s: unit %s requires type %s, got %s", key, u.suffix, u.typ, f.Type)
				}
				break
			}
		}
	}
}

func TestLookup(t *testing.T) {
	tests := []struct {
		key    string
		ok     bool
		typ    Type
		rateOf string
	}{
		{"ptp.offset_ns", true, Int, ""},
		{"sdi.crc_errors", true, Int, ""},
		{"sdi.crc_errors_per_s", true, Float, "sdi.crc_errors"},
		{"ptp.offset_ns_per_s", false, "", ""}, // kein Zähler
		{"present", false, "", ""},
		{"videoxlink.p2p", false, "", ""},
	}
	for _, tt := range tests {
		f, ok := Lookup(tt.key)
		if ok != tt.ok || f.Type != tt.typ || f.RateOf != tt.rateOf {
			t.Errorf("Lookup(%q) = %+v, %v; want ok %v, type %q, rate of %q", tt.key, f, ok, tt.ok, tt.typ, tt.rateOf)
		}
		if ok && f.Key() != tt.key {
			t.Errorf("Lookup(%q).Key() = %q", tt.key, f.Key())
		}
	}
}

func TestReserved(t *testing.T) {
	for _, m := range []string{"health", "rollup", "monika", "state", "ptp"} {
		if !Measurement(m) {
			t.Errorf("Measurement(%q) = false, want true", m)
		}
	}
	for _, k := range []string{"device", "group", "access"} {
		if !ReservedElementKind(k) || !ElementKind(k) {
			t.Errorf("%q must be a known, reserved kind", k)
		}
	}
	if ReservedElementKind("port") || !ElementKind("path") || ElementKind("gateway") {
		t.Error("unexpected kinds")
	}
	if !Transport("st2110-20") || !Transport("") || Transport("st2022-1") {
		t.Error("unexpected transports")
	}
}
