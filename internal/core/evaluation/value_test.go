package evaluation

import (
	"encoding/json"
	"testing"
)

func TestHolds(t *testing.T) {
	v := func(raw string) any {
		t.Helper()
		x, err := parseValue(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("parseValue(%s): %v", raw, err)
		}
		return x
	}
	tests := []struct {
		op     Op
		fact   any
		value  string
		want   bool
		wantOK bool
	}{
		{OpEq, true, `true`, true, true},
		{OpEq, false, `true`, false, true},
		{OpNe, "i", `"p"`, true, true},
		{OpEq, int64(50), `50`, true, true},
		{OpEq, 50.0, `50`, true, true},
		{OpEq, int64(50), `50.0`, true, true},
		{OpEq, int64(9007199254740993), `9007199254740992`, false, true}, // exakt als int64
		{OpLte, int64(80), `80`, true, true},
		{OpLt, int64(80), `80`, false, true},
		{OpLt, 79.5, `80`, true, true},
		{OpGt, int64(81), `80.5`, true, true},
		{OpGte, 80.5, `80.5`, true, true},
		{OpIn, 59.94, `[50, 59.94]`, true, true},
		{OpIn, int64(25), `[50, 59.94]`, false, true},
		{OpNotIn, "i", `["p", "psf"]`, true, true},
		{OpEq, "50", `50`, false, false},
		{OpLt, "a", `1`, false, false},
		{OpEq, true, `"true"`, false, false},
		{OpIn, true, `[1]`, false, false},
	}
	for _, tt := range tests {
		got, ok := holds(tt.op, tt.fact, v(tt.value))
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("holds(%v %s %s) = %v, %v; want %v, %v", tt.fact, tt.op, tt.value, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestParseValueErrors(t *testing.T) {
	for _, raw := range []string{``, `null`, `{"a": 1}`, `[[1]]`, `[null]`, `1 2`, `1e999`, `tru`} {
		if _, err := parseValue(json.RawMessage(raw)); err == nil {
			t.Errorf("parseValue(%s): want error", raw)
		}
	}
}

func TestConditionCheck(t *testing.T) {
	facts := map[string]any{"codec.bitrate_mbps": 25.0, "state.running": true}
	tests := []struct {
		c       Condition
		want    bool
		wantErr bool
	}{
		{Condition{Fact: "codec.bitrate_mbps", Op: OpEq, Value: json.RawMessage(`25`)}, true, false},
		{Condition{Fact: "codec.bitrate_mbps", Op: OpEq, Value: json.RawMessage(`20`)}, false, false},
		{Condition{Fact: "state.running", Op: OpEq, Value: json.RawMessage(`true`)}, true, false},
		{Condition{Fact: "state.missing", Op: OpEq, Value: json.RawMessage(`true`)}, false, false},
		{Condition{Fact: "state.running", Op: OpEq, Value: json.RawMessage(`25`)}, false, false}, // Typ passt nicht
		{Condition{Fact: "state.running", Op: OpEq, Value: json.RawMessage(`{`)}, false, true},
	}
	for _, tt := range tests {
		got, err := tt.c.Check(facts)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("Check(%s %s %s) = %v, %v", tt.c.Fact, tt.c.Op, tt.c.Value, got, err)
		}
	}
}
