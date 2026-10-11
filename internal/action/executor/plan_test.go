package executor

import (
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// world: Gerät X8 (Treiber fake) mit Zugang A, Modul M und den Ports P1, P2 und P3 (verschwunden);
// Gerät X9 (Treiber other) mit Zugang A2 und Modul M2; Gruppe G mit beiden Geräten.
type world struct {
	view                                *core.View
	x8, a, m, p1, p2, p3, x9, a2, m2, g uuid.UUID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{}
	for _, p := range []*uuid.UUID{&w.x8, &w.a, &w.m, &w.p1, &w.p2, &w.p3, &w.x9, &w.a2, &w.m2, &w.g} {
		*p = uuid.Must(uuid.NewV7())
	}
	src1, src2 := uuid.New(), uuid.New()
	settings := func(s map[string]sdk.Setting) json.RawMessage {
		b, _ := json.Marshal(s)
		return b
	}
	el := func(id, parent, device, source uuid.UUID, kind inventory.Kind, name string, actions ...string) inventory.Element {
		return inventory.Element{ID: id, Kind: kind, ParentID: parent, DeviceID: device, SourceID: source, Name: name, Actions: actions, Present: true, Monitored: true}
	}
	p1 := el(w.p1, w.m, w.x8, src1, "port", "P1", vocab.ActionSet)
	p1.Settings = settings(map[string]sdk.Setting{
		"codec.bitrate_mbps": {Type: sdk.SettingFloat},
		"fake.interface":     {Type: sdk.SettingEnum, WriteOnly: true},
		"ptp.domain":         {Type: sdk.SettingInt, Critical: true},
	})
	p2 := el(w.p2, w.m, w.x8, src1, "port", "P2", vocab.ActionSet, "fake.trigger")
	p2.Settings = settings(map[string]sdk.Setting{"codec.bitrate_mbps": {Type: sdk.SettingFloat}})
	p3 := el(w.p3, w.m, w.x8, src1, "port", "P3", vocab.ActionStart)
	p3.Present = false
	m := el(w.m, w.a, w.x8, src1, "module", "M", vocab.ActionStart, vocab.ActionStop)
	inv := inventory.New(nil, nil, slog.New(slog.DiscardHandler))
	inv.Load(inventory.Snapshot{
		Sources: []inventory.Source{
			{ID: src1, DeviceID: w.x8, Driver: "fake", Enabled: true, AccessElementID: w.a},
			{ID: src2, DeviceID: w.x9, Driver: "other", Enabled: true, AccessElementID: w.a2},
		},
		Elements: []inventory.Element{
			{ID: w.x8, Kind: inventory.KindDevice, DeviceID: w.x8, Label: "X8", Present: true, Monitored: true},
			el(w.a, w.x8, w.x8, src1, inventory.KindAccess, "fake", vocab.ActionReboot),
			m, p1, p2, p3,
			{ID: w.x9, Kind: inventory.KindDevice, DeviceID: w.x9, Label: "X9", Present: true, Monitored: true},
			el(w.a2, w.x9, w.x9, src2, inventory.KindAccess, "other", vocab.ActionReboot),
			el(w.m2, w.a2, w.x9, src2, "module", "M2", vocab.ActionStart),
			{ID: w.g, Kind: inventory.KindGroup, Label: "G", Present: true, Monitored: true},
		},
		Links: []inventory.Link{
			{ID: uuid.New(), Type: inventory.LinkMember, FromID: w.g, ToID: w.x8},
			{ID: uuid.New(), Type: inventory.LinkMember, FromID: w.g, ToID: w.x9},
		},
	})
	w.view = &core.View{Graph: inv.Graph()}
	return w
}

func testPlanner() planner {
	drivers := map[string]sdk.Driver{
		"fake": {Name: "fake", Actions: []sdk.ActionDef{
			{Name: vocab.ActionStart, Verify: []sdk.Condition{{Fact: vocab.StateRunning, Op: "eq", Value: "true"}}, Timeout: 5 * time.Second},
			{Name: vocab.ActionStop},
			{Name: vocab.ActionReboot, Critical: true},
			{Name: vocab.ActionSet},
			{Name: "fake.trigger", Verify: []sdk.Condition{{Fact: "fake.state", Op: "eq", Value: "{{state}}"}}},
		}},
		"other": {Name: "other", Actions: []sdk.ActionDef{{Name: vocab.ActionStart}, {Name: vocab.ActionReboot, Critical: true}}},
	}
	return planner{
		drivers: func(name string) (sdk.Driver, bool) { d, ok := drivers[name]; return d, ok },
		newID:   func() uuid.UUID { return uuid.Must(uuid.NewV7()) },
	}
}

var now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// shape beschreibt einen Plan kurz: pro Lauf Element und Status.
func (w *world) shape(p plan) []string {
	names := map[uuid.UUID]string{w.x8: "X8", w.a: "A", w.m: "M", w.p1: "P1", w.p2: "P2", w.p3: "P3", w.x9: "X9", w.a2: "A2", w.m2: "M2", w.g: "G", uuid.Nil: "-"}
	var out []string
	for _, r := range p.runs {
		n, ok := names[r.ElementID]
		if !ok {
			n = "?"
		}
		out = append(out, n+":"+string(r.Status))
	}
	return out
}

func TestPlanTargets(t *testing.T) {
	w := newWorld(t)
	tests := []struct {
		name string
		req  Request
		want []string
	}{
		{"element itself", Request{ElementID: w.m, Action: vocab.ActionStart}, []string{"M:pending"}},
		{"device level", Request{ElementID: w.x8, Action: vocab.ActionReboot, DeviceLevel: true, Driver: "fake"}, []string{"X8:pending", "A:pending"}},
		{"group, all others", Request{ElementID: w.g, Action: vocab.ActionStart}, []string{"G:pending", "M:pending", "P3:failed", "M2:pending"}},
		{"group, driver filter", Request{ElementID: w.g, Action: vocab.ActionStart, Driver: "other"}, []string{"G:pending", "M2:pending"}},
		{"set on device, only with all keys", Request{ElementID: w.x8, Action: vocab.ActionSet, Params: json.RawMessage(`{"codec.bitrate_mbps":20,"fake.interface":"eth7"}`)}, []string{"X8:pending", "P1:pending"}},
		{"frozen targets", Request{ElementID: w.g, Action: vocab.ActionStart, Targets: []uuid.UUID{w.m, uuid.New(), w.p1}}, []string{"G:pending", "M:pending", "?:failed"}},
		{"frozen, nothing left", Request{ElementID: w.g, Action: vocab.ActionStart, Targets: []uuid.UUID{w.p1}}, []string{"G:failed"}},
		{"preset root", Request{Action: ActionPreset}, []string{"-:pending"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := testPlanner().plan(w.view, tt.req, now)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if got := w.shape(p); !equal(got, tt.want) {
				t.Errorf("runs = %v, want %v", got, tt.want)
			}
			for _, r := range p.runs[1:] {
				if r.ParentID != p.runs[0].ID {
					t.Errorf("child %s has parent %s", r.ElementPath, r.ParentID)
				}
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlanFreezes(t *testing.T) {
	w := newWorld(t)
	p := testPlanner()

	start, _ := p.plan(w.view, Request{ElementID: w.m, Action: vocab.ActionStart, Actor: uuid.New()}, now)
	r := start.runs[0]
	if r.Critical || r.Verify == nil || r.Verify.Timeout != 5*time.Second || len(r.Verify.Conditions) != 1 ||
		r.ElementPath != "X8 / M" || r.SourceID == uuid.Nil || !r.RequestedAt.Equal(now) {
		t.Errorf("start run = %+v", r)
	}

	reboot, _ := p.plan(w.view, Request{ElementID: w.x8, Action: vocab.ActionReboot, DeviceLevel: true, Driver: "fake"}, now)
	if !reboot.runs[0].Critical || !reboot.runs[1].Critical || reboot.runs[1].Verify != nil {
		t.Errorf("reboot: parent critical %v, child %+v", reboot.runs[0].Critical, reboot.runs[1])
	}

	set, _ := p.plan(w.view, Request{ElementID: w.p1, Action: vocab.ActionSet, Params: json.RawMessage(`{"codec.bitrate_mbps":20,"fake.interface":"eth7"}`)}, now)
	v := set.runs[0].Verify
	if set.runs[0].Critical || v == nil || v.Timeout != defaultVerifyTimeout || len(v.Conditions) != 1 ||
		v.Conditions[0].Fact != "codec.bitrate_mbps" || string(v.Conditions[0].Value) != "20" {
		t.Errorf("set: critical %v, verify %+v", set.runs[0].Critical, v)
	}
	ptp, _ := p.plan(w.view, Request{ElementID: w.p1, Action: vocab.ActionSet, Params: json.RawMessage(`{"ptp.domain":1}`)}, now)
	if !ptp.runs[0].Critical {
		t.Error("set of a critical key is not critical")
	}

	trigger, _ := p.plan(w.view, Request{ElementID: w.p2, Action: "fake.trigger", Params: json.RawMessage(`{"state":"program"}`)}, now)
	if c := trigger.runs[0].Verify.Conditions[0]; string(c.Value) != `"program"` {
		t.Errorf("placeholder = %s", c.Value)
	}

	group, _ := p.plan(w.view, Request{ElementID: w.x8, Action: vocab.ActionSet, Params: json.RawMessage(`{"codec.bitrate_mbps":20,"fake.interface":"eth7"}`)}, now)
	var sum summary
	if err := json.Unmarshal(group.runs[0].Response, &sum); err != nil || sum.Targets != 1 || len(sum.Skipped) != 1 ||
		sum.Skipped[0].ElementID != w.p2 || sum.Skipped[0].Reason != "missing key fake.interface" || sum.Unsupported != 2 {
		t.Errorf("summary = %s, %v", group.runs[0].Response, err)
	}
	if f := group.runs[0]; f.Status != action.StatusPending {
		t.Errorf("parent status = %s", f.Status)
	}
}

func TestPlanErrors(t *testing.T) {
	w := newWorld(t)
	tests := []struct {
		name string
		req  Request
		want error
	}{
		{"unknown element", Request{ElementID: uuid.New(), Action: vocab.ActionStart}, ErrUnknownTarget},
		{"element without the action", Request{ElementID: w.m, Action: vocab.ActionReboot}, ErrUnsupported},
		{"element without the setting", Request{ElementID: w.p2, Action: vocab.ActionSet, Params: json.RawMessage(`{"ptp.domain":1}`)}, ErrUnsupported},
		{"nothing in scope", Request{ElementID: w.g, Action: vocab.ActionStop, Driver: "other"}, ErrUnsupported},
		{"device level without driver", Request{ElementID: w.g, Action: vocab.ActionReboot, DeviceLevel: true}, ErrDriverRequired},
		{"set without object", Request{ElementID: w.p1, Action: vocab.ActionSet, Params: json.RawMessage(`20`)}, ErrInvalidParams},
		{"set with empty object", Request{ElementID: w.p1, Action: vocab.ActionSet, Params: json.RawMessage(`{}`)}, ErrInvalidParams},
		{"missing placeholder", Request{ElementID: w.p2, Action: "fake.trigger", Params: json.RawMessage(`{}`)}, ErrInvalidParams},
		{"no action", Request{ElementID: w.m}, ErrInvalidParams},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := testPlanner().plan(w.view, tt.req, now); !errors.Is(err, tt.want) {
				t.Errorf("plan = %v, want %v", err, tt.want)
			}
		})
	}
}
