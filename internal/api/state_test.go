package api

import (
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// scene ist ein kleines Inventar: X8 mit Zugang A und den Ports P1, P2; X9 mit Zugang B und Port
// Q1; Gruppe G mit P1. P1 speist Q1, P2 zeigt auf ein unbekanntes Gerät.
type scene struct {
	x8, a, p1, p2, x9, b, q1, g uuid.UUID
	s8, s9                      uuid.UUID
	member, feeds, dangling     uuid.UUID
	ruleDevice, ruleGroup       uuid.UUID
	ruleGlobal                  uuid.UUID
	findingP1, findingQ1        uuid.UUID
	runP1, presetRoot           uuid.UUID
}

func newScene(t *testing.T) (*scene, *core.View) {
	t.Helper()
	id := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	sc := &scene{x8: id(), a: id(), p1: id(), p2: id(), x9: id(), b: id(), q1: id(), g: id(), s8: id(), s9: id(),
		member: id(), feeds: id(), dangling: id(), ruleDevice: id(), ruleGroup: id(), ruleGlobal: id(),
		findingP1: id(), findingQ1: id(), runP1: id(), presetRoot: id()}
	inv := inventory.New(nil, func(string) (sdk.Driver, bool) { return sdk.Driver{}, true }, slog.New(slog.DiscardHandler))
	inv.Load(sc.snapshot())
	rule := func(id, scope uuid.UUID, name, limit string) evaluation.Rule {
		return evaluation.Rule{ID: id, Name: name, Enabled: true, Severity: evaluation.SeverityWarning, ScopeElementID: scope,
			Fact: "system.temperature_c", Op: evaluation.OpLte, Value: json.RawMessage(limit)}
	}
	rules, errs := evaluation.NewRules([]evaluation.Rule{
		rule(sc.ruleDevice, sc.x8, "X8 warm", "90"),
		rule(sc.ruleGroup, sc.g, "Bühne warm", "70"),
		rule(sc.ruleGlobal, uuid.Nil, "warm", "80"),
	}, func(string) (vocab.Type, bool) { return vocab.Float, true })
	if len(errs) > 0 {
		t.Fatalf("NewRules: %v", errs)
	}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	v := &core.View{
		Version: 4711, Graph: inv.Graph(), Rules: rules,
		Facts: map[uuid.UUID]sdk.Facts{sc.p1: {"system.temperature_c": 95.0}, sc.q1: {"system.temperature_c": 40.0}},
		Health: map[uuid.UUID]evaluation.ElementHealth{
			sc.p1: {ElementID: sc.p1, Health: evaluation.HealthWarning},
			sc.q1: {ElementID: sc.q1, Health: evaluation.HealthOK},
			sc.g:  {ElementID: sc.g, Health: evaluation.HealthWarning, Rollup: &evaluation.Rollup{Warning: 1}},
		},
		Findings: map[uuid.UUID]evaluation.Finding{
			sc.findingP1: {ID: sc.findingP1, ElementID: sc.p1, Origin: evaluation.OriginRule, RuleID: sc.ruleDevice, Code: "rule.violated", Severity: evaluation.SeverityWarning, RaisedAt: now},
			sc.findingQ1: {ID: sc.findingQ1, ElementID: sc.q1, Origin: evaluation.OriginDevice, Code: "los", Severity: evaluation.SeverityError, RaisedAt: now},
		},
		Runs: map[uuid.UUID]action.Run{
			sc.runP1:      {ID: sc.runP1, ElementID: sc.p1, Action: "set", Status: action.StatusSent, RequestedAt: now, RequestedByID: boss.UserID},
			sc.presetRoot: {ID: sc.presetRoot, PresetID: uuid.New(), Action: "preset", Status: action.StatusPending, RequestedAt: now},
		},
		Sources: map[uuid.UUID]core.SourceStatus{
			sc.s8: {State: core.StateLive, Since: now, Heartbeat: 5 * time.Second},
			sc.s9: {State: core.StateUnreachable, Since: now, Heartbeat: 5 * time.Second, Error: "timeout"},
		},
	}
	return sc, v
}

// snapshot ist das Inventar der Szene.
func (sc *scene) snapshot() inventory.Snapshot {
	el := func(id, parent, device, source uuid.UUID, kind inventory.Kind, name string) inventory.Element {
		ext := ""
		if kind == "port" {
			ext = "sdi/" + name[len(name)-1:] // SDI 1 → sdi/1
		}
		return inventory.Element{ID: id, ParentID: parent, DeviceID: device, SourceID: source, Kind: kind, Name: name,
			ExternalID: ext, Present: true, Monitored: true, Attributes: json.RawMessage(`{"slot":1}`)}
	}
	return inventory.Snapshot{
		Sources: []inventory.Source{
			{ID: sc.s8, DeviceID: sc.x8, Driver: "videoxlink", Enabled: true, AccessElementID: sc.a},
			{ID: sc.s9, DeviceID: sc.x9, Driver: "videoxlink", Enabled: true, AccessElementID: sc.b},
		},
		Elements: []inventory.Element{
			el(sc.x8, uuid.Nil, sc.x8, uuid.Nil, inventory.KindDevice, ""),
			el(sc.a, sc.x8, sc.x8, sc.s8, inventory.KindAccess, "videoxlink"),
			el(sc.p1, sc.a, sc.x8, sc.s8, "port", "SDI 1"),
			el(sc.p2, sc.a, sc.x8, sc.s8, "port", "SDI 2"),
			el(sc.x9, uuid.Nil, sc.x9, uuid.Nil, inventory.KindDevice, ""),
			el(sc.b, sc.x9, sc.x9, sc.s9, inventory.KindAccess, "videoxlink"),
			el(sc.q1, sc.b, sc.x9, sc.s9, "port", "SDI 1"),
			el(sc.g, uuid.Nil, uuid.Nil, uuid.Nil, inventory.KindGroup, ""),
		},
		Links: []inventory.Link{
			{ID: sc.member, Type: inventory.LinkMember, FromID: sc.g, ToID: sc.p1},
			{ID: sc.feeds, Type: inventory.LinkFeeds, SourceID: sc.s8, FromID: sc.p1, ToID: sc.q1},
			{ID: sc.dangling, Type: inventory.LinkFeeds, SourceID: sc.s8, FromID: sc.p2, ToRef: "xlink:REMOTE"},
		},
	}
}

// ref ist ein Eintrag aus /state, nur mit seinen IDs.
type ref struct {
	ID        uuid.UUID
	ElementID uuid.UUID `json:"element_id"`
	SourceID  uuid.UUID `json:"source_id"`
}

type stateResponse struct {
	Version                                                    int64
	Elements, Skeleton, Links, Health, Findings, Runs, Sources []ref
}

func ids(items []ref, id func(ref) uuid.UUID) []uuid.UUID {
	out := []uuid.UUID{}
	for _, it := range items {
		out = append(out, id(it))
	}
	slices.SortFunc(out, compareIDs)
	return out
}

func sorted(in ...uuid.UUID) []uuid.UUID {
	slices.SortFunc(in, compareIDs)
	return nonNil(in)
}

func TestState(t *testing.T) {
	sc, v := newScene(t)
	s := newTestServer(t)
	s.cfg.Core.(*fakeCore).view.Store(v)
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{
		anna.UserID: {{Permission: auth.PermView, ScopeElementID: sc.g}, {Permission: auth.PermOperate}},
		boss.UserID: {{Permission: auth.PermView}, {Permission: auth.PermPresetRun}},
	}
	all := sorted(sc.x8, sc.a, sc.p1, sc.p2, sc.x9, sc.b, sc.q1, sc.g)
	tests := []struct {
		name, cookie                                               string
		elements, skeleton, links, health, findings, runs, sources []uuid.UUID
	}{
		// anna sieht die Gruppe und P1, Gerät und Zugang als Gerüst; nur den Link in der Gruppe,
		// keine Quelle (ihr Zugang ist Gerüst) und keine Wurzel eines Presets.
		{"group only", "anna", sorted(sc.g, sc.p1), sorted(sc.a, sc.x8), sorted(sc.member), sorted(sc.p1, sc.g),
			sorted(sc.findingP1), sorted(sc.runP1), sorted()},
		{"everything", "boss", all, sorted(), sorted(sc.member, sc.feeds, sc.dangling), sorted(sc.p1, sc.q1, sc.g),
			sorted(sc.findingP1, sc.findingQ1), sorted(sc.runP1, sc.presetRoot), sorted(sc.s8, sc.s9)},
	}
	byID := func(r ref) uuid.UUID { return r.ID }
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := call(t, s, "GET", "/api/v1/state", "", tt.cookie)
			var st stateResponse
			if err := json.Unmarshal(w.Body.Bytes(), &st); w.Code != 200 || err != nil || st.Version != 4711 {
				t.Fatalf("state = %d %s", w.Code, w.Body)
			}
			for _, c := range []struct {
				what      string
				got, want []uuid.UUID
			}{
				{"elements", ids(st.Elements, byID), tt.elements},
				{"skeleton", ids(st.Skeleton, byID), tt.skeleton},
				{"links", ids(st.Links, byID), tt.links},
				{"health", ids(st.Health, func(r ref) uuid.UUID { return r.ElementID }), tt.health},
				{"findings", ids(st.Findings, byID), tt.findings},
				{"runs", ids(st.Runs, byID), tt.runs},
				{"sources", ids(st.Sources, func(r ref) uuid.UUID { return r.SourceID }), tt.sources},
			} {
				if !slices.Equal(c.got, c.want) {
					t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
				}
			}
		})
	}
	// Freie JSON-Felder gehen unverändert durch.
	if w := call(t, s, "GET", "/api/v1/state", "", "anna"); !strings.Contains(w.Body.String(), `"attributes":{"slot":1}`) {
		t.Errorf("attributes not passed through: %s", w.Body)
	}
}

func TestElementFactsAndRules(t *testing.T) {
	sc, v := newScene(t)
	s := newTestServer(t)
	s.cfg.Core.(*fakeCore).view.Store(v)
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{anna.UserID: {{Permission: auth.PermView, ScopeElementID: sc.g}}}

	w := call(t, s, "GET", "/api/v1/elements/"+sc.p1.String()+"/facts", "", "anna")
	if w.Code != 200 || w.Body.String() != `{"element_id":"`+sc.p1.String()+`","facts":{"system.temperature_c":95},"version":4711}`+"\n" {
		t.Errorf("facts = %d %s", w.Code, w.Body)
	}
	for _, hidden := range []uuid.UUID{sc.q1, sc.a, uuid.New()} { // unsichtbar, Gerüst, unbekannt
		if w := call(t, s, "GET", "/api/v1/elements/"+hidden.String()+"/facts", "", "anna"); w.Code != 404 {
			t.Errorf("facts of %s = %d", hidden, w.Code)
		}
		if w := call(t, s, "GET", "/api/v1/elements/"+hidden.String()+"/rules", "", "anna"); w.Code != 404 {
			t.Errorf("rules of %s = %d", hidden, w.Code)
		}
	}

	// Am Gerät schlägt die Gruppe, die schlägt global. Den Geltungsbereich X8 sieht anna nur als
	// Gerüst: Art ja, ID nein. Die Regel am Gerät hat einen aktiven Befund.
	w = call(t, s, "GET", "/api/v1/elements/"+sc.p1.String()+"/rules", "", "anna")
	var sets []struct {
		Fact, Direction       string
		Effective, Overridden []struct {
			RuleID         uuid.UUID `json:"rule_id"`
			Scope          string
			ScopeElementID *uuid.UUID `json:"scope_element_id"`
			Satisfied      *bool
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sets); w.Code != 200 || err != nil || len(sets) != 1 {
		t.Fatalf("rules = %d %s", w.Code, w.Body)
	}
	set := sets[0]
	if set.Fact != "system.temperature_c" || set.Direction != "upper" || len(set.Effective) != 1 || len(set.Overridden) != 2 {
		t.Fatalf("rule set = %s", w.Body)
	}
	if e := set.Effective[0]; e.RuleID != sc.ruleDevice || e.Scope != "device" || e.ScopeElementID != nil || e.Satisfied == nil || *e.Satisfied {
		t.Errorf("effective = %+v", e)
	}
	if o := set.Overridden[0]; o.RuleID != sc.ruleGroup || o.Scope != "group" || o.ScopeElementID == nil || *o.ScopeElementID != sc.g || o.Satisfied != nil {
		t.Errorf("overridden group rule = %+v", o)
	}
	if o := set.Overridden[1]; o.RuleID != sc.ruleGlobal || o.Scope != "global" || o.ScopeElementID != nil {
		t.Errorf("overridden global rule = %+v", o)
	}
}

func TestDrivers(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Drivers = func() []sdk.Driver {
		return []sdk.Driver{{
			Name:    "videoxlink",
			Actions: []sdk.ActionDef{{Name: "videoxlink.reboot", Critical: true, Timeout: 90 * time.Second}},
			Facts:   []sdk.FactDef{{Key: "videoxlink.rx_errors", Type: vocab.Int, Kind: vocab.Counter, Description: "Fehler"}},
		}}
	}
	w := call(t, s, "GET", "/api/v1/drivers", "", "anna")
	var drivers []struct {
		Name         string
		ConfigSchema json.RawMessage `json:"config_schema"`
		Actions      []struct {
			Name           string
			Critical       bool
			TimeoutSeconds float64 `json:"timeout_seconds"`
		}
		Facts []struct {
			Key, Type, Kind string
			Historized      bool
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &drivers); w.Code != 200 || err != nil || len(drivers) != 1 {
		t.Fatalf("drivers = %d %s", w.Code, w.Body)
	}
	d := drivers[0]
	if string(d.ConfigSchema) != "{}" || len(d.Actions) != 1 || d.Actions[0].TimeoutSeconds != 90 || !d.Actions[0].Critical {
		t.Errorf("driver = %+v", d)
	}
	keys := map[string]bool{}
	for _, f := range d.Facts {
		keys[f.Key] = f.Historized
	}
	if h, ok := keys["videoxlink.rx_errors"]; !ok || h {
		t.Errorf("raw counter missing or historized")
	}
	if _, ok := keys["videoxlink.rx_errors"+vocab.RateSuffix]; !ok {
		t.Error("rate of the raw counter missing")
	}
	if len(keys) <= len(vocab.Fields()) {
		t.Errorf("facts = %d, want the vocabulary plus the raw facts", len(keys))
	}
}
