package sdk

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lukirs95/monika/sdk/vocab"
)

func testDriver() Driver {
	return Driver{
		Name: "acme",
		New:  func(SourceConfig) (Source, error) { return nil, nil },
		Actions: []ActionDef{
			{Name: vocab.ActionStart, Verify: []Condition{{Fact: vocab.StateRunning, Op: "eq", Value: "true"}}, Timeout: 10 * time.Second},
			{Name: vocab.ActionReboot, Critical: true},
			{Name: "acme.reset_ptp"},
		},
		Facts: []FactDef{
			{Key: "acme.fpga_build", Type: vocab.String, Kind: vocab.Status},
			{Key: "acme.overruns", Type: vocab.Int, Kind: vocab.Counter},
		},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Driver)
		want   string // "" = gültig
	}{
		{"valid", func(*Driver) {}, ""},
		{"name with dash", func(d *Driver) { d.Name = "directout-ravio" }, "[a-z0-9]+"},
		{"name is a measurement", func(d *Driver) { d.Name = "ptp" }, "measurement"},
		{"no New", func(d *Driver) { d.New = nil }, "missing constructor"},
		{"raw fact without prefix", func(d *Driver) { d.Facts[0].Key = "fpga_build" }, "raw facts are acme."},
		{"duplicate fact", func(d *Driver) { d.Facts[1] = d.Facts[0] }, "declared twice"},
		{"counter not int", func(d *Driver) { d.Facts[1].Type = vocab.Float }, "counters are int"},
		{"declared rate", func(d *Driver) { d.Facts[0].Key = "acme.overruns_per_s" }, "rates are derived"},
		{"raw action without prefix", func(d *Driver) { d.Actions[2].Name = "reset_ptp" }, "raw actions are acme."},
		{"verify on unknown fact", func(d *Driver) { d.Actions[0].Verify[0].Fact = "running" }, "unknown fact"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDriver()
			tt.change(&d)
			err := d.validate()
			if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("validate() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestFactType(t *testing.T) {
	d := testDriver()
	tests := []struct {
		key  string
		want FactType
		ok   bool
	}{
		{vocab.StateRunning, vocab.Bool, true},
		{"sdi.crc_errors_per_s", vocab.Float, true},
		{"acme.fpga_build", vocab.String, true},
		{"acme.overruns_per_s", vocab.Float, true},
		{"acme.fpga_build_per_s", "", false},
		{"other.thing", "", false},
	}
	for _, tt := range tests {
		if got, ok := d.FactType(tt.key); got != tt.want || ok != tt.ok {
			t.Errorf("FactType(%q) = %q, %v; want %q, %v", tt.key, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCheckElement(t *testing.T) {
	d := testDriver()
	valid := func() Element {
		return Element{
			ExternalID: "sender/1", Kind: vocab.ElementSender, Transport: "st2110-20", Direction: vocab.DirectionOut,
			Actions: []string{vocab.ActionStart, vocab.ActionReboot}, CriticalActions: []string{vocab.ActionReboot},
			Settings: map[string]Setting{
				"stream.dest_port":      {Type: SettingInt},
				"stream.dest_address":   {Type: SettingIPv4},
				"acme.label_on_display": {Type: SettingString, WriteOnly: true},
			},
		}
	}
	tests := []struct {
		name   string
		change func(*Element)
		want   string
	}{
		{"valid", func(*Element) {}, ""},
		{"empty external ID", func(e *Element) { e.ExternalID = "" }, "external ID is empty"},
		{"reserved kind", func(e *Element) { e.Kind = vocab.ElementAccess }, "reserved"},
		{"unknown kind", func(e *Element) { e.Kind = "gateway" }, "unknown kind"},
		{"unknown transport", func(e *Element) { e.Transport = "st2022-1" }, "unknown transport"},
		{"action not in catalog", func(e *Element) { e.Actions = append(e.Actions, vocab.ActionStop) }, "not in the catalog"},
		{"critical action not offered", func(e *Element) { e.CriticalActions = []string{"acme.reset_ptp"} }, "not an action of the element"},
		{"setting type mismatch", func(e *Element) { e.Settings["stream.dest_port"] = Setting{Type: SettingFloat} }, "does not match"},
		{"setting without fact", func(e *Element) { e.Settings["stream.nope"] = Setting{Type: SettingInt} }, "no such fact"},
		{"write-only with fact", func(e *Element) { e.Settings["stream.dest_port"] = Setting{Type: SettingInt, WriteOnly: true} }, "write-only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := valid()
			tt.change(&e)
			err := d.CheckElement(e)
			if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("CheckElement() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

type nopSource struct{}

func (nopSource) Heartbeat() time.Duration                         { return 0 }
func (nopSource) Run(context.Context, Reporter) error              { return nil }
func (nopSource) Execute(context.Context, Command) (Result, error) { return Result{}, nil }

func TestRegister(t *testing.T) {
	d := testDriver()
	d.Name = "registrytest"
	d.Actions, d.Facts = nil, nil
	d.New = func(SourceConfig) (Source, error) { return nopSource{}, nil }
	Register(d)
	if got, ok := Lookup("registrytest"); !ok || got.Name != d.Name {
		t.Fatalf("Lookup = %+v, %v", got, ok)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("second Register did not panic")
		}
	}()
	Register(d)
}
