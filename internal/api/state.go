package api

import (
	"bytes"
	"context"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/api/oapi"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Aktueller Stand aus der View, gefiltert nach Rechten (api.md, Stand, Ereignisse und Abos).

var errNotFound = problem(http.StatusNotFound, "not_found", "unknown or not visible")

// viewFor liefert die aktuelle View und den angemeldeten Benutzer.
func (s *Server) viewFor(ctx context.Context) (*core.View, auth.Principal, error) {
	v, err := s.view()
	return v, principal(ctx), err
}

// view liefert die aktuelle View; ohne sie läuft der Core noch nicht.
func (s *Server) view() (*core.View, error) {
	v := s.cfg.Core.View()
	if v == nil {
		return nil, core.ErrStopped
	}
	return v, nil
}

func (s *Server) GetState(ctx context.Context, _ oapi.GetStateRequestObject) (oapi.GetStateResponseObject, error) {
	v, err := s.view()
	if err != nil {
		return nil, err
	}
	si := s.sight(v, principal(ctx))
	out := oapi.GetState200JSONResponse{
		Version: int64(v.Version), Elements: []oapi.Element{}, Skeleton: []oapi.SkeletonElement{},
		Links: []oapi.Link{}, Health: []oapi.ElementHealth{}, Findings: []oapi.Finding{},
		Runs: []oapi.RunSummary{}, Sources: []oapi.SourceStatus{},
	}
	for id, e := range v.Graph.All() {
		if si.sees(id) {
			out.Elements = append(out.Elements, toElement(e))
			if h, ok := v.Health[id]; ok {
				out.Health = append(out.Health, toHealth(h))
			}
		}
	}
	for _, id := range si.skeleton() {
		e, _ := v.Graph.Element(id)
		out.Skeleton = append(out.Skeleton, toSkeleton(e))
	}
	for _, l := range v.Graph.Links() {
		if si.seesLink(l) {
			out.Links = append(out.Links, toLink(l))
		}
	}
	for _, f := range v.Findings {
		if si.sees(f.ElementID) {
			out.Findings = append(out.Findings, toFinding(f))
		}
	}
	for _, r := range v.Runs {
		if si.seesRun(r.ElementID) {
			out.Runs = append(out.Runs, toRunSummary(r))
		}
	}
	for id, src := range v.Graph.Sources() {
		if si.sees(src.AccessElementID) {
			out.Sources = append(out.Sources, toSourceStatus(id, v.Sources[id]))
		}
	}
	// Feste Reihenfolge, damit gleiche Stände gleich aussehen.
	slices.SortFunc(out.Elements, func(a, b oapi.Element) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(out.Skeleton, func(a, b oapi.SkeletonElement) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(out.Links, func(a, b oapi.Link) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(out.Health, func(a, b oapi.ElementHealth) int { return compareIDs(a.ElementID, b.ElementID) })
	slices.SortFunc(out.Findings, func(a, b oapi.Finding) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(out.Runs, func(a, b oapi.RunSummary) int { return compareIDs(a.ID, b.ID) })
	slices.SortFunc(out.Sources, func(a, b oapi.SourceStatus) int { return compareIDs(a.SourceID, b.SourceID) })
	return out, nil
}

func (s *Server) GetElementFacts(ctx context.Context, rq oapi.GetElementFactsRequestObject) (oapi.GetElementFactsResponseObject, error) {
	v, err := s.view()
	if err != nil {
		return nil, err
	}
	if !s.sight(v, principal(ctx)).sees(rq.ElementID) {
		return nil, errNotFound
	}
	return oapi.GetElementFacts200JSONResponse(elementFacts(v, rq.ElementID)), nil
}

// elementFacts sind die Fakten eines Elements mit der Version der View.
func elementFacts(v *core.View, id uuid.UUID) oapi.ElementFacts {
	facts := oapi.Facts{}
	for k, val := range v.Facts[id] {
		facts[k] = val
	}
	return oapi.ElementFacts{Version: int64(v.Version), ElementID: id, Facts: facts}
}

func (s *Server) GetElementRules(ctx context.Context, rq oapi.GetElementRulesRequestObject) (oapi.GetElementRulesResponseObject, error) {
	v, err := s.view()
	if err != nil {
		return nil, err
	}
	si := s.sight(v, principal(ctx))
	if !si.sees(rq.ElementID) {
		return nil, errNotFound
	}
	// Erfüllt ist eine geltende Regel, solange sie keinen aktiven Befund am Element hat; das ist
	// das Urteil des Evaluators mit Haltezeit und Hysterese.
	violated := map[uuid.UUID]bool{}
	for _, f := range v.Findings {
		if f.ElementID == rq.ElementID && f.Origin == evaluation.OriginRule {
			violated[f.RuleID] = true
		}
	}
	ref := func(r evaluation.Rule, effective bool) oapi.RuleRef {
		out := oapi.RuleRef{RuleID: r.ID, Name: optional(r.Name), Scope: scopeClass(v.Graph, rq.ElementID, r.ScopeElementID)}
		if si.sees(r.ScopeElementID) {
			out.ScopeElementID = optionalID(r.ScopeElementID)
		}
		if effective {
			out.Satisfied = new(!violated[r.ID])
		}
		return out
	}
	out := oapi.GetElementRules200JSONResponse{}
	for _, rs := range v.EffectiveRules(rq.ElementID) {
		set := oapi.EffectiveRuleSet{
			Fact: rs.Fact, Direction: oapi.EffectiveRuleSetDirection(rs.Direction),
			Effective: []oapi.RuleRef{}, Overridden: []oapi.RuleRef{},
		}
		for _, r := range rs.Rules {
			set.Effective = append(set.Effective, ref(r, true))
		}
		for _, r := range rs.Overridden {
			set.Overridden = append(set.Overridden, ref(r, false))
		}
		out = append(out, set)
	}
	return out, nil
}

// scopeClass ist die Art des Geltungsbereichs einer Regel aus Sicht des Elements.
func scopeClass(g *inventory.Graph, element, scope uuid.UUID) oapi.RuleRefScope {
	if scope == uuid.Nil {
		return oapi.RuleRefScopeGlobal
	}
	if scope == element {
		return oapi.RuleRefScopeElement
	}
	switch e, _ := g.Element(scope); e.Kind {
	case inventory.KindGroup:
		return oapi.RuleRefScopeGroup
	case inventory.KindDevice:
		return oapi.RuleRefScopeDevice
	}
	return oapi.RuleRefScopeAncestor
}

func (s *Server) ListDrivers(context.Context, oapi.ListDriversRequestObject) (oapi.ListDriversResponseObject, error) {
	out := oapi.ListDrivers200JSONResponse{}
	for _, d := range s.cfg.Drivers() {
		out = append(out, toDriver(d))
	}
	return out, nil
}

// toDriver beschreibt einen Treiber mit seinem Fakt-Katalog: erst das Vokabular, dann die rohen
// Fakten des Treibers, jeweils mit den Raten der Zähler.
func toDriver(d sdk.Driver) oapi.Driver {
	out := oapi.Driver{Name: d.Name, ConfigSchema: d.ConfigSchema, Actions: []oapi.ActionDef{}, Facts: []oapi.FactDef{}}
	if len(out.ConfigSchema) == 0 {
		out.ConfigSchema = []byte(`{}`)
	}
	for _, a := range d.Actions {
		def := oapi.ActionDef{Name: a.Name, Params: a.Params, Critical: a.Critical}
		if a.Timeout > 0 {
			def.TimeoutSeconds = new(a.Timeout.Seconds())
		}
		out.Actions = append(out.Actions, def)
	}
	fact := func(key string, t vocab.Type, k vocab.Kind, historized bool, desc string) {
		out.Facts = append(out.Facts, oapi.FactDef{
			Key: key, Type: oapi.FactDefType(t), Kind: oapi.FactDefKind(k), Historized: historized, Description: optional(desc),
		})
		if k == vocab.Counter {
			// Die Rate historisiert der Influx-Schreiber nur, wenn das Element den Zähler nicht meldet.
			out.Facts = append(out.Facts, oapi.FactDef{
				Key: key + vocab.RateSuffix, Type: oapi.FactDefType(vocab.Float), Kind: oapi.FactDefKind(vocab.Gauge), Historized: historized,
			})
		}
	}
	for _, f := range vocab.Fields() {
		fact(f.Key(), f.Type, f.Kind, f.Historized, "")
	}
	for _, f := range d.Facts {
		fact(f.Key, f.Type, f.Kind, false, f.Description)
	}
	return out
}

func compareIDs(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }
