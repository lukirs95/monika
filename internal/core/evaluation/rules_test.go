package evaluation

import (
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// testTypes ist ein kleiner Fakt-Katalog für die Tests.
func testTypes(key string) (vocab.Type, bool) {
	t, ok := map[string]vocab.Type{
		"temp": vocab.Float, "crc": vocab.Int, "signal": vocab.Bool, "running": vocab.Bool,
		"scan": vocab.String, vocab.Present: vocab.Bool,
	}[key]
	return t, ok
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestCompileRule(t *testing.T) {
	valid := func() Rule {
		return Rule{ID: uuid.New(), Name: "Temperatur", Enabled: true, Severity: SeverityWarning, Fact: "temp", Op: OpLte, Value: raw(`80`)}
	}
	tests := []struct {
		name   string
		change func(*Rule)
		want   string // "" = gültig
	}{
		{"valid", func(*Rule) {}, ""},
		{"int value for float fact", func(r *Rule) { r.Value = raw(`80`) }, ""},
		{"hysteresis", func(r *Rule) { r.ClearValue = raw(`75`) }, ""},
		{"when", func(r *Rule) { r.When = &Condition{Fact: "running", Op: OpEq, Value: raw(`true`)} }, ""},
		{"in list", func(r *Rule) { r.Fact, r.Op, r.Value = "scan", OpIn, raw(`["i", "p"]`) }, ""},
		{"filters", func(r *Rule) { r.MatchKind, r.MatchTransport, r.MatchDriver = "access", "sdi", "videoxlink" }, ""},
		{"no name", func(r *Rule) { r.Name = "" }, "name is empty"},
		{"unknown severity", func(r *Rule) { r.Severity = "fatal" }, "unknown severity"},
		{"negative hold", func(r *Rule) { r.HoldSeconds = -1 }, "must not be negative"},
		{"unknown kind", func(r *Rule) { r.MatchKind = "gateway" }, "unknown kind"},
		{"unknown transport", func(r *Rule) { r.MatchTransport = "st2022-1" }, "unknown transport"},
		{"unknown fact", func(r *Rule) { r.Fact = "nope" }, "unknown fact"},
		{"unknown op", func(r *Rule) { r.Op = "like" }, "unknown operator"},
		{"bound on bool", func(r *Rule) { r.Fact, r.Value = "signal", raw(`true`) }, "needs a number"},
		{"string for number", func(r *Rule) { r.Op, r.Value = OpEq, raw(`"80"`) }, "does not fit"},
		{"empty list", func(r *Rule) { r.Op, r.Value = OpIn, raw(`[]`) }, "non-empty list"},
		{"list of wrong type", func(r *Rule) { r.Op, r.Value = OpIn, raw(`[1, "a"]`) }, "does not fit"},
		{"hysteresis on equality", func(r *Rule) { r.Op, r.ClearValue = OpEq, raw(`75`) }, "upper or lower bound"},
		{"hysteresis on wrong side", func(r *Rule) { r.ClearValue = raw(`85`) }, "wrong side"},
		{"hysteresis on wrong side of lower bound", func(r *Rule) { r.Op, r.ClearValue = OpGte, raw(`75`) }, "wrong side"},
		{"bad when", func(r *Rule) { r.When = &Condition{Fact: "running", Op: OpEq, Value: raw(`1`)} }, "when: value 1 does not fit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid()
			tt.change(&r)
			_, err := compileRule(r, testTypes)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("compileRule: %v", err)
			case tt.want != "" && (!errors.Is(err, ErrInvalidRule) || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("compileRule = %v, want ErrInvalidRule containing %q", err, tt.want)
			}
		})
	}
}

func TestViolated(t *testing.T) {
	mustRule := func(r Rule) *rule {
		t.Helper()
		r.Name, r.Severity = "r", SeverityError
		c, err := compileRule(r, testTypes)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	temp := mustRule(Rule{Fact: "temp", Op: OpLte, Value: raw(`80`), ClearValue: raw(`75`)})
	signal := mustRule(Rule{Fact: "signal", Op: OpEq, Value: raw(`true`), When: &Condition{Fact: "running", Op: OpEq, Value: raw(`true`)}})
	tests := []struct {
		name         string
		r            *rule
		facts        sdk.Facts
		active       bool
		want, wantOK bool
	}{
		{"within limit", temp, sdk.Facts{"temp": 70.0}, false, false, true},
		{"above limit", temp, sdk.Facts{"temp": 81.0}, false, true, true},
		{"active, between clear and limit", temp, sdk.Facts{"temp": 78.0}, true, true, true},
		{"active, below clear value", temp, sdk.Facts{"temp": 75.0}, true, false, true},
		{"fact missing", temp, sdk.Facts{}, false, false, false},
		{"fact of wrong type", temp, sdk.Facts{"temp": "hot"}, false, false, false},
		{"when holds, no signal", signal, sdk.Facts{"running": true, "signal": false}, false, true, true},
		{"when does not hold", signal, sdk.Facts{"running": false, "signal": false}, false, false, true},
		{"when fact missing", signal, sdk.Facts{"signal": false}, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.r.violated(func(k string) (any, bool) { v, ok := tt.facts[k]; return v, ok }, tt.active)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("violated = %v, %v; want %v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// fakeState ist die Attrappe von State über einen echten Graph des Inventars. Quellen sind Live,
// außer sie stehen in notLive.
type fakeState struct {
	graph       *inventory.Graph
	facts       map[uuid.UUID]sdk.Facts
	notLive     map[uuid.UUID]bool
	unreachable map[uuid.UUID]string
	heartbeat   time.Duration
}

func (s *fakeState) Graph() *inventory.Graph           { return s.graph }
func (s *fakeState) Facts(id uuid.UUID) sdk.Facts      { return s.facts[id] }
func (s *fakeState) Live(id uuid.UUID) bool            { return !s.notLive[id] }
func (s *fakeState) Heartbeat(uuid.UUID) time.Duration { return s.heartbeat }
func (s *fakeState) Unreachable(id uuid.UUID) (string, bool) {
	r, ok := s.unreachable[id]
	return r, ok
}

// world ist ein kleines Inventar: Gerät X8 mit Zugang und SDI-Port, Gruppe g1 ∋ Port,
// Gruppe g2 ∋ g1, dazu ein verschwundener und ein nicht überwachter Port. Nach einer Änderung
// an snap baut rebuild den Graph neu.
type world struct {
	state                                              *fakeState
	snap                                               inventory.Snapshot
	source                                             uuid.UUID
	device, access, port, missing, unmonitored, g1, g2 uuid.UUID
}

func (w *world) rebuild() {
	inv := inventory.New(nil, sdk.Lookup, slog.New(slog.DiscardHandler))
	inv.Load(w.snap)
	w.state.graph = inv.Graph()
}

// change ändert ein Element und baut den Graph neu.
func (w *world) change(id uuid.UUID, f func(*inventory.Element)) {
	for i := range w.snap.Elements {
		if w.snap.Elements[i].ID == id {
			f(&w.snap.Elements[i])
		}
	}
	w.rebuild()
}

// remove löscht ein Element und baut den Graph neu.
func (w *world) remove(id uuid.UUID) {
	w.snap.Elements = slices.DeleteFunc(w.snap.Elements, func(e inventory.Element) bool { return e.ID == id })
	w.rebuild()
}

// setSource ändert die Quelle und baut den Graph neu.
func (w *world) setSource(f func(*inventory.Source)) {
	f(&w.snap.Sources[0])
	w.rebuild()
}

func newWorld(t *testing.T) *world {
	t.Helper()
	id := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	w := &world{source: id(), device: id(), access: id(), port: id(), missing: id(), unmonitored: id(), g1: id(), g2: id()}
	source := w.source
	el := func(eid, parent uuid.UUID, kind inventory.Kind, present, monitored bool) inventory.Element {
		return inventory.Element{ID: eid, Kind: kind, ParentID: parent, DeviceID: w.device, SourceID: source, Transport: "sdi", Name: "e", Present: present, Monitored: monitored}
	}
	w.snap = inventory.Snapshot{
		Sources: []inventory.Source{{ID: source, DeviceID: w.device, Driver: "videoxlink", Enabled: true, AccessElementID: w.access}},
		Elements: []inventory.Element{
			{ID: w.device, Kind: inventory.KindDevice, DeviceID: w.device, Label: "X8", Present: true, Monitored: true},
			el(w.access, w.device, inventory.KindAccess, true, true),
			el(w.port, w.access, "port", true, true),
			el(w.missing, w.access, "port", false, true),
			el(w.unmonitored, w.access, "port", true, false),
			{ID: w.g1, Kind: inventory.KindGroup, Present: true, Monitored: true},
			{ID: w.g2, Kind: inventory.KindGroup, Present: true, Monitored: true},
		},
		Links: []inventory.Link{
			{ID: id(), Type: inventory.LinkMember, FromID: w.g1, ToID: w.port},
			{ID: id(), Type: inventory.LinkMember, FromID: w.g2, ToID: w.g1},
		},
	}
	facts := sdk.Facts{"temp": 70.0, "signal": true, "crc": int64(0), "running": true}
	w.state = &fakeState{facts: map[uuid.UUID]sdk.Facts{w.port: facts, w.unmonitored: facts}}
	w.rebuild()
	return w
}

func TestEffectiveRules(t *testing.T) {
	w := newWorld(t)
	r := func(name string, scope uuid.UUID, fact string, op Op, value string, change ...func(*Rule)) Rule {
		rule := Rule{ID: uuid.New(), Name: name, Enabled: true, Severity: SeverityError, ScopeElementID: scope, Fact: fact, Op: op, Value: raw(value)}
		for _, c := range change {
			c(&rule)
		}
		return rule
	}
	byDriver := func(r *Rule) { r.MatchDriver = "videoxlink" }
	rules, errs := NewRules([]Rule{
		r("a global", uuid.Nil, "temp", OpLte, `80`),
		r("b global with filter", uuid.Nil, "temp", OpLte, `82`, byDriver),
		r("c device", w.device, "temp", OpLte, `85`),
		r("d device lower bound", w.device, "temp", OpGte, `10`),
		r("e element warning", w.port, "temp", OpLte, `90`),
		r("f element error", w.port, "temp", OpLte, `95`),
		r("g group", w.g1, "signal", OpEq, `true`),
		r("h global", uuid.Nil, "signal", OpEq, `true`),
		r("i global equality", uuid.Nil, "crc", OpEq, `0`),
		r("j element bound", w.port, "crc", OpLte, `100`),
		r("k outer group", w.g2, "signal", OpEq, `true`),
		r("l fact missing", uuid.Nil, "scan", OpEq, `"i"`),
		r("m disabled", w.port, "temp", OpLte, `99`, func(r *Rule) { r.Enabled = false }),
		r("n other kind", uuid.Nil, "temp", OpLte, `99`, func(r *Rule) { r.MatchKind = "sender" }),
		r("o when fact missing", uuid.Nil, "crc", OpEq, `0`, func(r *Rule) { r.When = &Condition{Fact: "scan", Op: OpEq, Value: raw(`"i"`)} }),
		r("p present", uuid.Nil, vocab.Present, OpEq, `true`),
		r("z invalid", uuid.Nil, "nope", OpEq, `true`),
	}, testTypes)
	if len(errs) != 1 {
		t.Fatalf("NewRules errors = %v, want one for the invalid rule", errs)
	}

	names := func(rules []Rule) string {
		var out []string
		for _, r := range rules {
			out = append(out, r.Name[:1])
		}
		return strings.Join(out, ",")
	}
	summary := func(sets []RuleSet) []string {
		var out []string
		for _, s := range sets {
			out = append(out, s.Fact+"/"+string(s.Direction)+": "+names(s.Rules)+" over "+names(s.Overridden))
		}
		return out
	}
	tests := []struct {
		name string
		id   uuid.UUID
		want []string
	}{
		{"port", w.port, []string{
			"crc/equal: i over ",
			"crc/upper: j over ",
			"present/equal: p over ",
			"signal/equal: g over h,k",
			"temp/lower: d over ",
			"temp/upper: e,f over a,b,c",
		}},
		{"missing element keeps present", w.missing, []string{"present/equal: p over "}},
		{"unmonitored element", w.unmonitored, nil},
		{"unknown element", uuid.New(), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summary(rules.EffectiveRules(w.state, tt.id)); !slices.Equal(got, tt.want) {
				t.Errorf("EffectiveRules =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}
