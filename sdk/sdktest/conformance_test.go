package sdktest

import (
	"strings"
	"testing"
	"time"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

func TestConformanceFake(t *testing.T) {
	f := NewFake("fake")
	f.Device("a", gw4()).SetHeartbeat(0)
	Conformance(t, f.Driver(), sdk.SourceConfig{Address: "a"}, Options{
		Commands: []sdk.Command{
			{ExternalID: "sender/1", Action: vocab.ActionStart},
			{ExternalID: "sender/1", Action: vocab.ActionStop},
			{ExternalID: "in/1", Action: vocab.ActionStart},
		},
	})
}

func TestConformanceViolations(t *testing.T) {
	quick := Options{Timeout: time.Second, StopTimeout: 100 * time.Millisecond}
	tests := []struct {
		name  string
		setup func(f *Fake) sdk.Driver
		want  string
	}{
		{"config schema is broken", func(f *Fake) sdk.Driver {
			f.Device("a", gw4()).SetHeartbeat(0)
			d := f.Driver()
			d.ConfigSchema = []byte(`{"type":"objekt"}`)
			return d
		}, "ConfigSchema"},
		{"config does not match the schema", func(f *Fake) sdk.Driver {
			f.Device("a", gw4()).SetHeartbeat(0)
			d := f.Driver()
			d.ConfigSchema = []byte(`{"type":"object","required":["port"]}`)
			return d
		}, "does not match ConfigSchema"},
		{"reserved kind", func(f *Fake) sdk.Driver {
			s := gw4()
			s.Elements[0].Kind = vocab.ElementAccess
			f.Device("a", s).SetHeartbeat(0)
			return f.Driver()
		}, "reserved"},
		{"unknown fact", func(f *Fake) sdk.Driver {
			s := gw4()
			s.Facts["in/1"] = sdk.Facts{"signal_present": true}
			f.Device("a", s).SetHeartbeat(0)
			return f.Driver()
		}, "neither in the vocabulary nor in the catalog"},
		{"wrong Go type", func(f *Fake) sdk.Driver {
			s := gw4()
			s.Facts[""] = sdk.Facts{"system.uptime_s": 100.0}
			f.Device("a", s).SetHeartbeat(0)
			return f.Driver()
		}, "must be int, got float64"},
		{"present reported", func(f *Fake) sdk.Driver {
			s := gw4()
			s.Facts["in/1"] = sdk.Facts{vocab.Present: true}
			f.Device("a", s).SetHeartbeat(0)
			return f.Driver()
		}, "reserved"},
		{"represents by external ID", func(f *Fake) sdk.Driver {
			s := gw4()
			s.Links = append(s.Links, sdk.Link{Type: sdk.LinkRepresents, From: sdk.Endpoint{ExternalID: "in/1"}, To: sdk.Endpoint{ExternalID: "sender/1"}})
			f.Device("a", s).SetHeartbeat(0)
			return f.Driver()
		}, "represents must point to a global_ref"},
		{"foreign namespace", func(f *Fake) sdk.Driver {
			s := gw4()
			s.GlobalRef = "xlink:X8A1001"
			f.Device("a", s).SetHeartbeat(0)
			return f.Driver()
		}, "does not belong to driver"},
		{"no sign of life", func(f *Fake) sdk.Driver {
			f.Device("a", gw4()).SetHeartbeat(10 * time.Millisecond)
			return f.Driver()
		}, "no sign of life"},
		{"hangs after cancel", func(f *Fake) sdk.Driver {
			dev := f.Device("a", gw4())
			dev.SetHeartbeat(0)
			dev.Hang()
			t.Cleanup(dev.Release)
			return f.Driver()
		}, "did not return"},
		{"unstable external IDs", func(f *Fake) sdk.Driver {
			f.Device("a", gw4()).SetHeartbeat(0)
			other := gw4()
			other.Elements[0].ExternalID = "input-1"
			other.Links, other.Facts = nil, nil
			f.Device("b", other).SetHeartbeat(0)
			d := f.Driver()
			calls := 0
			newSource := d.New
			d.New = func(cfg sdk.SourceConfig) (sdk.Source, error) {
				calls++
				if calls > 1 {
					cfg.Address = "b"
				}
				return newSource(cfg)
			}
			return d
		}, "external IDs differ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.setup(NewFake("fake"))
			errs := check(t.Context(), d, sdk.SourceConfig{Address: "a"}, quick)
			for _, err := range errs {
				if strings.Contains(err.Error(), tt.want) {
					return
				}
			}
			t.Fatalf("check() = %v, want an error containing %q", errs, tt.want)
		})
	}
}
