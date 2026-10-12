package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core/coretest"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
)

// commandWorld ist die Szene in einem laufenden Core. anna sieht und bearbeitet die Gruppe G
// (mit P1), boss darf alles, viewer sieht alles und darf nichts.
func commandWorld(t *testing.T) (*scene, *Server, *coretest.Running) {
	t.Helper()
	sc, _ := newScene(t)
	running := coretest.Start(t, coretest.Options{
		Snapshot: sc.snapshot(),
		Drivers: []sdk.Driver{
			{Name: "videoxlink", ConfigSchema: []byte(`{"type":"object","properties":{"port":{"type":"integer"}}}`)},
			{Name: "ipmi"},
		},
		Findings: []evaluation.Finding{
			{ID: sc.findingP1, ElementID: sc.p1, Origin: evaluation.OriginDevice, Code: "los", Severity: evaluation.SeverityError, RaisedAt: time.Now()},
			{ID: sc.findingQ1, ElementID: sc.q1, Origin: evaluation.OriginDevice, Code: "los", Severity: evaluation.SeverityError, RaisedAt: time.Now()},
		},
	})
	s := newServerWith(t, running)
	s.cfg.Auth.(*fakeAuth).grants = map[uuid.UUID][]auth.Grant{
		anna.UserID: {
			{Permission: auth.PermView, ScopeElementID: sc.g}, {Permission: auth.PermInventoryEdit, ScopeElementID: sc.g},
			{Permission: auth.PermRuleEdit, ScopeElementID: sc.g}, {Permission: auth.PermAcknowledge, ScopeElementID: sc.g},
		},
		boss.UserID: {
			{Permission: auth.PermView}, {Permission: auth.PermInventoryEdit}, {Permission: auth.PermSourceEdit},
			{Permission: auth.PermRuleEdit}, {Permission: auth.PermPresetRun},
		},
		viewer.UserID: {{Permission: auth.PermView}},
	}
	return sc, s, running
}

// step ist eine Anfrage mit erwarteter Antwort; want ist ein Teil des Bodies.
type step struct {
	name, method, path, body, who string
	status                        int
	want                          string
}

func run(t *testing.T, s *Server, steps []step) map[string]json.RawMessage {
	t.Helper()
	bodies := map[string]json.RawMessage{}
	for _, st := range steps {
		w := call(t, s, st.method, st.path, st.body, st.who)
		if w.Code != st.status || !strings.Contains(w.Body.String(), st.want) {
			t.Fatalf("%s: %s %s = %d %s, want %d with %s", st.name, st.method, st.path, w.Code, w.Body, st.status, st.want)
		}
		bodies[st.name] = w.Body.Bytes()
	}
	return bodies
}

func idOf(t *testing.T, body json.RawMessage) string {
	t.Helper()
	var v struct{ ID string }
	if err := json.Unmarshal(body, &v); err != nil || v.ID == "" {
		t.Fatalf("no id in %s", body)
	}
	return v.ID
}

func TestDevicesAndSources(t *testing.T) {
	sc, s, running := commandWorld(t)
	b := run(t, s, []step{
		{"create device", "POST", "/api/v1/devices", `{"label":"X10"}`, "boss", 201, `"kind":"device"`},
		{"anna may not", "POST", "/api/v1/devices", `{"label":"X11"}`, "anna", 403, `"code":"forbidden"`},
		{"viewer may not", "POST", "/api/v1/devices", `{"label":"X11"}`, "viewer", 403, `"code":"forbidden"`},
	})
	x10 := idOf(t, b["create device"])
	running.Stores.InUse[sc.x9] = []inventory.PresetRef{{ID: uuid.New(), Name: "Show A"}}

	b = run(t, s, []step{
		{"config against the schema", "POST", "/api/v1/devices/" + x10 + "/sources", `{"driver":"videoxlink","config":{"port":"x"},"enabled":true}`, "boss", 422, `"code":"invalid_config"`},
		{"unknown driver", "POST", "/api/v1/devices/" + x10 + "/sources", `{"driver":"acme","config":{},"enabled":true}`, "boss", 422, `"code":"unknown_driver"`},
		{"create source", "POST", "/api/v1/devices/" + x10 + "/sources", `{"driver":"videoxlink","address":"10.0.0.10","config":{"port":443},"enabled":true}`, "boss", 201, `"address":"10.0.0.10"`},
		{"second source with the driver", "POST", "/api/v1/devices/" + x10 + "/sources", `{"driver":"videoxlink","config":{},"enabled":true}`, "boss", 409, `"code":"source_exists"`},
		{"anna sees no sources of a skeleton device", "GET", "/api/v1/devices/" + sc.x8.String() + "/sources", "", "anna", 404, ""},
		{"list sources", "GET", "/api/v1/devices/" + sc.x8.String() + "/sources", "", "viewer", 200, `"access_element_id":"` + sc.a.String() + `"`},
		{"disable a source", "PUT", "/api/v1/sources/" + sc.s8.String(), `{"config":{"port":80},"enabled":false}`, "boss", 200, `"enabled":false`},
		{"source_edit is global", "PUT", "/api/v1/sources/" + sc.s8.String(), `{"config":{},"enabled":true}`, "viewer", 403, ""},
		{"device in use", "DELETE", "/api/v1/devices/" + sc.x9.String(), "", "boss", 409, `"presets":[{"id"`},
		{"delete device", "DELETE", "/api/v1/devices/" + x10, "", "boss", 204, ""},
		{"deleted", "DELETE", "/api/v1/devices/" + x10, "", "boss", 404, ""},
	})
	if v := running.View(); v.Sources[sc.s8].State != "disabled" {
		t.Errorf("source after disabling = %+v", v.Sources[sc.s8])
	}
	src := idOf(t, b["create source"])
	if _, ok := running.View().Graph.Source(uuid.MustParse(src)); ok {
		t.Error("source of the deleted device still there")
	}
}

func TestGroupsAndUserFields(t *testing.T) {
	sc, s, running := commandWorld(t)
	g, p1, p2 := sc.g.String(), sc.p1.String(), sc.p2.String()
	b := run(t, s, []step{
		{"create group", "POST", "/api/v1/groups", `{"label":"Studio"}`, "boss", 201, `"kind":"group"`},
		{"invisible member", "POST", "/api/v1/groups/" + g + "/members", `{"element_id":"` + p2 + `"}`, "anna", 404, ""},
		{"add member", "POST", "/api/v1/groups/" + g + "/members", `{"element_id":"` + p2 + `","position":2}`, "boss", 201, `"type":"member"`},
		{"member twice", "POST", "/api/v1/groups/" + g + "/members", `{"element_id":"` + p2 + `"}`, "boss", 409, `"code":"member_exists"`},
		{"update membership", "PUT", "/api/v1/memberships/" + sc.member.String(), `{"position":1,"label":"Cam"}`, "anna", 200, `"label":"Cam"`},
		{"user fields", "PUT", "/api/v1/elements/" + p1 + "/user-fields", `{"label":"Cam 1","notes":null,"monitored":true}`, "anna", 200, `"label":"Cam 1"`},
		{"user fields need the right", "PUT", "/api/v1/elements/" + p1 + "/user-fields", `{"label":"x","notes":null,"monitored":true}`, "viewer", 403, ""},
		{"remove membership", "DELETE", "/api/v1/memberships/" + sc.member.String(), "", "anna", 204, ""},
		{"member gone, P1 out of sight", "PUT", "/api/v1/elements/" + p1 + "/user-fields", `{"label":"x","notes":null,"monitored":true}`, "anna", 404, ""},
	})
	studio := idOf(t, b["create group"])
	run(t, s, []step{{"delete group", "DELETE", "/api/v1/groups/" + studio, "", "boss", 204, ""}})
	if e, _ := running.View().Graph.Element(sc.p1); e.Label != "Cam 1" {
		t.Errorf("P1 = %+v", e)
	}
}

func TestRulesAndAck(t *testing.T) {
	sc, s, running := commandWorld(t)
	g := sc.g.String()
	rule := func(scope string, limit int) string {
		return `{"name":"warm","enabled":true,"severity":"warning","scope_element_id":` + scope +
			`,"fact":"system.temperature_c","op":"lte","value":` + itoa(limit) + `,"clear_value":` + itoa(limit-5) + `.5,"hold_seconds":10}`
	}
	b := run(t, s, []step{
		{"ack", "POST", "/api/v1/findings/" + sc.findingP1.String() + "/ack", "", "anna", 204, ""},
		{"ack invisible", "POST", "/api/v1/findings/" + sc.findingQ1.String() + "/ack", "", "anna", 404, ""},
		{"ack needs the right", "POST", "/api/v1/findings/" + sc.findingQ1.String() + "/ack", "", "viewer", 403, ""},
		{"rule in the group", "POST", "/api/v1/rules", rule(`"`+g+`"`, 80), "anna", 201, `"clear_value":75.5`},
		{"global rule needs a global right", "POST", "/api/v1/rules", rule("null", 80), "anna", 403, ""},
		{"rule on an invisible scope", "POST", "/api/v1/rules", rule(`"`+sc.x9.String()+`"`, 80), "anna", 404, ""},
		{"unknown fact", "POST", "/api/v1/rules", strings.Replace(rule("null", 80), "system.temperature_c", "nope", 1), "boss", 422, `"code":"invalid_rule"`},
		{"global rule", "POST", "/api/v1/rules", rule("null", 90), "boss", 201, `"value":90`},
	})
	grp, global := idOf(t, b["rule in the group"]), idOf(t, b["global rule"])
	if f := running.View().Findings[sc.findingP1]; f.AckedByID != anna.UserID {
		t.Errorf("finding after ack = %+v", f)
	}
	b = run(t, s, []step{
		{"list for anna", "GET", "/api/v1/rules", "", "anna", 200, grp},
		{"change", "PUT", "/api/v1/rules/" + grp, rule(`"`+g+`"`, 70), "anna", 200, `"value":70`},
		{"move out of reach", "PUT", "/api/v1/rules/" + grp, rule("null", 70), "anna", 403, ""},
		{"global rule is visible but not editable", "DELETE", "/api/v1/rules/" + global, "", "anna", 403, ""},
		{"delete", "DELETE", "/api/v1/rules/" + grp, "", "anna", 204, ""},
		{"deleted", "DELETE", "/api/v1/rules/" + grp, "", "anna", 404, ""},
	})
	var rules []struct{ ID string }
	if err := json.Unmarshal(b["list for anna"], &rules); err != nil || len(rules) != 2 {
		t.Errorf("rules for anna = %s", b["list for anna"])
	}
}

func itoa(i int) string { return strings.TrimSpace(string(must(json.Marshal(i)))) }

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
