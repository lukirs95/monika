package http

import (
	"encoding/json"
	"testing"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// BenchmarkPrepare misst, was ein trigger vor dem Senden kostet: Parameter prüfen, Vorlagen
// einsetzen, Anfrage bauen.
func BenchmarkPrepare(b *testing.B) {
	s := newTestSource(b, "http://h:8000", tallyConfig, "")
	c := s.calls["tally"]
	params := json.RawMessage(`{"camera": 12, "state": "program"}`)
	for b.Loop() {
		values, rejected, _ := c.values(params)
		if rejected != nil {
			b.Fatal(rejected)
		}
		if _, err := s.request(b.Context(), c, values, ""); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTrigger ist ein ganzer Aufruf über Loopback an die Attrappe, ohne und mit Anmeldung
// (Token im Speicher).
func BenchmarkTrigger(b *testing.B) {
	cmd := sdk.Command{ExternalID: "tally", Action: vocab.ActionTrigger, Params: json.RawMessage(`{"camera": 12}`)}
	b.Run("plain", func(b *testing.B) {
		tg := newTarget(b)
		defer tg.Close()
		s := newTestSource(b, tg.URL, tallyConfig, "")
		for b.Loop() {
			if res, err := s.Execute(b.Context(), cmd); err != nil || res.Status != sdk.Accepted {
				b.Fatal(res, err)
			}
			<-s.observed
		}
	})
	b.Run("auth", func(b *testing.B) {
		lt := newLoginTarget(b, "header")
		defer lt.Close()
		s := newTestSource(b, lt.URL, authConfig(`{"from": "header", "name": "X-Auth-Token"}`, bearer, "on_demand"), loginSecret)
		x := sdk.Command{ExternalID: "x", Action: vocab.ActionTrigger}
		for b.Loop() {
			if res, err := s.Execute(b.Context(), x); err != nil || res.Status != sdk.Accepted {
				b.Fatal(res, err)
			}
			<-s.observed
		}
	})
}
