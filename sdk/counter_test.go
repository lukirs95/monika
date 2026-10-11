package sdk

import (
	"testing"
	"time"
)

func TestCounterRate(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	type sample struct {
		raw  int64
		at   time.Duration
		rate float64
		ok   bool
	}
	tests := []struct {
		name    string
		width   int
		samples []sample
	}{
		{"first value has no rate", 0, []sample{{1200, 0, 0, false}}},
		{"rate per second", 0, []sample{{1200, 0, 0, false}, {1260, 2 * time.Second, 30, true}}},
		{"smaller value is a reset", 0, []sample{{1200, 0, 0, false}, {5, time.Second, 0, true}, {15, 2 * time.Second, 10, true}}},
		{"same time has no rate", 0, []sample{{1, 0, 0, false}, {2, 0, 0, false}}},
		{"32 bit overflow", 32, []sample{{1<<32 - 10, 0, 0, false}, {10, time.Second, 20, true}}},
		{"64 bit overflow", 64, []sample{{-10, 0, 0, false}, {10, time.Second, 20, true}}}, // -10 = 2⁶⁴-10 als uint64
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Counter{Width: tt.width}
			for i, s := range tt.samples {
				rate, ok := c.Rate(s.raw, t0.Add(s.at))
				if rate != s.rate || ok != s.ok {
					t.Fatalf("sample %d: Rate(%d) = %v, %v; want %v, %v", i, s.raw, rate, ok, s.rate, s.ok)
				}
			}
		})
	}
}
