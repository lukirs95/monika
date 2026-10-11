package evaluation

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
)

// Codes der Befunde, die Monika selbst vergibt.
const (
	CodeRuleViolated      = "rule.violated"
	CodeSourceUnreachable = "source.unreachable"
	CodeDiscoveryFailed   = "source.discovery_failed"
)

// writeTimeout begrenzt jeden Schreibvorgang aus der Schleife (docs/contracts/core.md, C3).
const writeTimeout = 2 * time.Second

// FindingChange ist eine Änderung an den aktiven Befunden: entstanden, beendet oder neu als
// Ursache bzw. Symptom zugeordnet.
type FindingChange struct {
	Raised   *Finding    // entstanden
	Ended    uuid.UUID   // beendet, mit Reason
	Reason   ClearReason // bei Ended
	CauseSet uuid.UUID   // Befund mit neuer Zuordnung zu CausedBy
	CausedBy uuid.UUID   // bei CauseSet: die Wurzel; uuid.Nil = selbst Ursache
}

// Evaluator bewertet Regeln, Gerätealarme und Systemzustände und führt die aktiven Befunde. Wie
// das Inventar ist er Logik ohne eigene Goroutinen; Besitzer ist die Schleife des Core. Er
// schreibt erst in die Datenbank und ändert dann den Speicher.
type Evaluator struct {
	store   Store
	types   FactTypes
	drivers func(name string) bool
	log     *slog.Logger
	newID   func() uuid.UUID

	rules  *Rules
	graph  *inventory.Graph // zuletzt gesehen, um Gelöschtes aufzuräumen
	active map[uuid.UUID]Finding
	tracks map[uuid.UUID]map[findingKey]*track // Element → seine Befunde in Arbeit und aktiv

	alarms       map[uuid.UUID][]sdk.Alarm // Element → zuletzt gemeldete Alarme
	discoveryErr map[uuid.UUID]string      // Quelle → Fehler des letzten Versuchs, einen Snapshot zu speichern

	causeGraph *inventory.Graph // Graph der letzten Zuordnung von Ursache und Symptom
	causeDirty bool             // ein SetFindingCause ist gescheitert

	health      map[uuid.UUID]ElementHealth // copy-on-write
	healthDirty map[uuid.UUID]bool          // Elemente mit geänderten Befunden
	healthGraph *inventory.Graph            // Graph und Regeln der letzten vollständigen Berechnung
	healthRules *Rules
	unsaved     map[uuid.UUID]ElementHealth // geändert, noch nicht gespeichert
}

// New liefert einen Evaluator ohne Regeln und Befunde; types ist meist RegistryFactTypes,
// drivers meist RegistryDriver.
func New(store Store, types FactTypes, drivers func(name string) bool, log *slog.Logger) *Evaluator {
	return &Evaluator{
		store: store, types: types, drivers: drivers, log: log,
		newID:        func() uuid.UUID { return uuid.Must(uuid.NewV7()) },
		rules:        newRules(nil),
		active:       map[uuid.UUID]Finding{},
		tracks:       map[uuid.UUID]map[findingKey]*track{},
		alarms:       map[uuid.UUID][]sdk.Alarm{},
		discoveryErr: map[uuid.UUID]string{},
		health:       map[uuid.UUID]ElementHealth{},
		healthDirty:  map[uuid.UUID]bool{},
		unsaved:      map[uuid.UUID]ElementHealth{},
	}
}

// findingKey ist ein Befund, der höchstens einmal aktiv sein kann (finding_active_uq).
type findingKey struct {
	element uuid.UUID
	rule    uuid.UUID // uuid.Nil bei Gerät und System
	code    string
}

func keyOf(f Finding) findingKey { return findingKey{f.ElementID, f.RuleID, f.Code} }

func compareKeys(a, b findingKey) int {
	return cmp.Or(slices.Compare(a.element[:], b.element[:]), slices.Compare(a.rule[:], b.rule[:]), strings.Compare(a.code, b.code))
}

type phase int

const (
	phasePending  phase = iota // verletzt, Haltezeit läuft; noch kein Befund
	phaseRaising               // soll entstehen, Schreiben steht aus (nicht sichtbar)
	phaseActive                // aktiv, in der Datenbank
	phaseClearing              // aktiv, erfüllt; clear_hold_seconds läuft
)

// track ist ein Befund in Arbeit oder aktiv. finding ist bis zum Entstehen die Vorlage.
type track struct {
	key     findingKey
	phase   phase
	since   time.Time   // Beginn von Pending bzw. Clearing
	finding Finding     // ab Raising mit ID
	ending  ClearReason // != "": Ende soll geschrieben werden, steht aus
}

func (t *track) isActive() bool {
	return t != nil && (t.phase == phaseActive || t.phase == phaseClearing)
}

// wanted ist ein Befund, der laut Bewertung sein soll (violated) oder dessen Grund zwar gilt,
// aber erfüllt ist. keep lässt ihn unverändert (source.unreachable, solange die Quelle noch
// nicht wieder Live ist).
type wanted struct {
	violated        bool
	keep            bool
	finding         Finding // Vorlage
	hold, clearHold time.Duration
}

// result sammelt die Änderungen und Fehler einer Verarbeitung.
type result struct {
	changes []FindingChange
	errs    []error
}

// ErrWrite kennzeichnet gescheiterte Schreibvorgänge von Befunden, Zuordnungen und Health. Sie
// werden im Takt wiederholt; für den Aufrufer betreffen sie den Zustand der Datenbank, nicht
// den Befehl.
var ErrWrite = errors.New("write failed")

func (r *result) done() ([]FindingChange, error) {
	if len(r.errs) == 0 {
		return r.changes, nil
	}
	return r.changes, fmt.Errorf("%w: %w", ErrWrite, errors.Join(r.errs...))
}

// Load übernimmt Regeln, aktive Befunde und gespeicherte Health beim Start. Regeln, die nicht
// mehr zum Katalog passen, bleiben mit Warn-Log draußen.
func (e *Evaluator) Load(rules []Rule, active []Finding, health []ElementHealth) {
	e.health = make(map[uuid.UUID]ElementHealth, len(health))
	for _, h := range health {
		e.health[h.ElementID] = h
	}
	rs, errs := NewRules(rules, e.types)
	for _, err := range errs {
		e.log.Warn("rule ignored", "err", err)
	}
	e.rules = rs
	e.active = make(map[uuid.UUID]Finding, len(active))
	e.tracks = map[uuid.UUID]map[findingKey]*track{}
	for _, f := range active {
		e.active[f.ID] = f
		e.put(&track{key: keyOf(f), phase: phaseActive, finding: f})
	}
}

// Restore lädt Regeln, aktive Befunde und gespeicherte Health aus dem Store (beim Start).
func (e *Evaluator) Restore(ctx context.Context) error {
	rules, err := e.store.ListRules(ctx)
	if err != nil {
		return fmt.Errorf("list rules: %w", err)
	}
	active, err := e.store.ListActiveFindings(ctx)
	if err != nil {
		return fmt.Errorf("list active findings: %w", err)
	}
	health, err := e.store.LoadHealth(ctx)
	if err != nil {
		return fmt.Errorf("load health: %w", err)
	}
	e.Load(rules, active, health)
	return nil
}

// Flush schreibt die noch nicht gespeicherte Health (beim Beenden der Schleife).
func (e *Evaluator) Flush(ctx context.Context) error {
	var res result
	e.saveHealth(ctx, &res)
	_, err := res.done()
	return err
}

// Rules liefert den aktuellen Satz von Regeln, Findings eine Kopie der aktiven Befunde.
func (e *Evaluator) Rules() *Rules                   { return e.rules }
func (e *Evaluator) Findings() map[uuid.UUID]Finding { return maps.Clone(e.active) }

// Evaluate bewertet die Elemente neu, etwa nach geänderten Fakten oder Änderungen am Inventar.
func (e *Evaluator) Evaluate(ctx context.Context, now time.Time, elements []uuid.UUID, s State) ([]FindingChange, error) {
	var res result
	e.prune(s.Graph(), &res)
	for _, id := range elements {
		e.reconcile(ctx, now, id, s, "", &res)
	}
	return e.finish(ctx, s.Graph(), &res)
}

// Tick lässt Haltezeiten ablaufen und wiederholt gescheiterte Schreibvorgänge.
func (e *Evaluator) Tick(ctx context.Context, now time.Time, s State) ([]FindingChange, error) {
	var res result
	g := s.Graph()
	e.prune(g, &res)
	for _, tr := range e.sortedTracks() {
		hold, clearHold, ok := e.holds(tr.key)
		switch {
		case tr.ending != "":
			e.end(ctx, tr, tr.ending, &res)
		case tr.phase == phaseRaising:
			e.raise(ctx, now, g, tr, &res)
		case !ok || !e.live(s, tr.key.element):
		case tr.phase == phasePending && !now.Before(tr.since.Add(hold)):
			e.raise(ctx, now, g, tr, &res)
		case tr.phase == phaseClearing && !now.Before(tr.since.Add(clearHold)):
			e.end(ctx, tr, ReasonResolved, &res)
		}
	}
	e.saveHealth(ctx, &res)
	// Lebenszeichen: Systembefunde aller Zugänge.
	for _, src := range slices.SortedFunc(maps.Values(maps.Collect(g.Sources())), func(a, b inventory.Source) int {
		return slices.Compare(a.ID[:], b.ID[:])
	}) {
		e.reconcile(ctx, now, src.AccessElementID, s, OriginSystem, &res)
	}
	return e.finish(ctx, g, &res)
}

// reconcile gleicht die Befunde eines Elements mit dem ab, was laut Bewertung sein soll; only
// beschränkt das auf eine Herkunft ("" = alle).
func (e *Evaluator) reconcile(ctx context.Context, now time.Time, id uuid.UUID, s State, only Origin, res *result) {
	g := s.Graph()
	el, exists := g.Element(id)
	eligible := exists && g.Monitored(id)
	includes := func(o Origin) bool { return only == "" || only == o }

	// Welche Herkünfte jetzt bewertet werden; die übrigen bleiben unverändert.
	evaluated := map[Origin]bool{}
	want := map[findingKey]wanted{}
	switch {
	case !eligible:
		for _, o := range []Origin{OriginRule, OriginDevice, OriginSystem} {
			evaluated[o] = includes(o)
		}
	default:
		if includes(OriginSystem) {
			evaluated[OriginSystem] = true
			e.wantSystem(el, s, want)
		}
		if !e.live(s, id) {
			break
		}
		if includes(OriginRule) {
			evaluated[OriginRule] = true
			e.wantRules(id, s, want)
		}
		if includes(OriginDevice) {
			evaluated[OriginDevice] = true
			if el.Present { // mit dem Element entfallen seine Alarme
				e.wantAlarms(id, want)
			}
		}
	}

	// Erst beenden, was nicht mehr gilt, dann das Neue: in dieser Reihenfolge sehen es auch die
	// Abonnenten.
	for _, tr := range e.elementTracks(id) {
		if _, ok := want[tr.key]; ok || !evaluated[tr.finding.Origin] {
			continue
		}
		e.drop(ctx, tr, e.reason(g, s, tr), res)
	}
	for _, key := range slices.SortedFunc(maps.Keys(want), compareKeys) {
		e.apply(ctx, now, g, key, want[key], res)
	}
}

// wantRules trägt die geltenden Regeln des Elements ein, verletzt oder erfüllt.
func (e *Evaluator) wantRules(id uuid.UUID, s State, want map[findingKey]wanted) {
	t, ok := newTarget(s, id)
	if !ok {
		return
	}
	for _, set := range e.rules.sets(t) {
		for _, r := range set.rules {
			key := findingKey{id, r.ID, CodeRuleViolated}
			v, ok := r.violated(t.fact, e.track(key).isActive())
			if !ok {
				continue
			}
			want[key] = wanted{
				violated:  v,
				finding:   ruleFinding(r, t),
				hold:      time.Duration(r.HoldSeconds) * time.Second,
				clearHold: time.Duration(r.ClearHoldSeconds) * time.Second,
			}
		}
	}
}

// wantAlarms trägt die zuletzt gemeldeten Alarme des Elements ein; jeder Code einmal.
func (e *Evaluator) wantAlarms(id uuid.UUID, want map[findingKey]wanted) {
	for _, a := range e.alarms[id] {
		key := findingKey{id, uuid.Nil, a.Code}
		if _, dup := want[key]; dup || a.Code == "" {
			continue
		}
		sev := Severity(a.Severity)
		if sev != SeverityInfo && sev != SeverityError {
			sev = SeverityWarning
		}
		want[key] = wanted{violated: true, finding: Finding{Origin: OriginDevice, Code: a.Code, Severity: sev, Message: a.Message}}
	}
}

// wantSystem trägt am Zugang source.unreachable und source.discovery_failed ein. Ein
// source.unreachable endet erst, wenn die Quelle wieder Live ist.
func (e *Evaluator) wantSystem(el inventory.Element, s State, want map[findingKey]wanted) {
	if el.Kind != inventory.KindAccess {
		return
	}
	src := el.SourceID
	key := findingKey{el.ID, uuid.Nil, CodeSourceUnreachable}
	reason, unreachable := s.Unreachable(src)
	switch {
	case unreachable:
		want[key] = wanted{violated: true, finding: systemFinding(CodeSourceUnreachable, map[string]string{"reason": reason})}
	case s.Live(src):
		want[key] = wanted{}
	default:
		want[key] = wanted{keep: true}
	}
	key = findingKey{el.ID, uuid.Nil, CodeDiscoveryFailed}
	if msg, failed := e.discoveryErr[src]; failed {
		want[key] = wanted{violated: true, finding: systemFinding(CodeDiscoveryFailed, map[string]string{"error": msg})}
	} else {
		want[key] = wanted{}
	}
}

func systemFinding(code string, details map[string]string) Finding {
	b, _ := json.Marshal(details) // map[string]string lässt sich immer kodieren
	return Finding{Origin: OriginSystem, Code: code, Severity: SeverityError, Details: b}
}

// apply führt einen Befund, dessen Grund gilt, zum gewünschten Zustand.
func (e *Evaluator) apply(ctx context.Context, now time.Time, g *inventory.Graph, key findingKey, w wanted, res *result) {
	tr := e.track(key)
	if w.keep {
		return
	}
	if w.violated {
		switch {
		case tr == nil:
			tr = &track{key: key, phase: phasePending, since: now, finding: w.finding}
			e.put(tr)
			if w.hold <= 0 {
				e.raise(ctx, now, g, tr, res)
			}
		case tr.phase == phasePending:
			tr.finding = w.finding // mit dem aktuellen Messwert
		case tr.phase == phaseClearing:
			tr.phase = phaseActive
		}
		return
	}
	switch {
	case tr == nil, tr.ending != "":
	case tr.phase == phasePending, tr.phase == phaseRaising:
		e.remove(tr)
	case tr.phase == phaseActive && w.clearHold <= 0:
		e.end(ctx, tr, ReasonResolved, res)
	case tr.phase == phaseActive:
		tr.phase, tr.since = phaseClearing, now
	}
}

// drop beendet einen Befund, dessen Grund nicht mehr gilt; einer in Arbeit verschwindet still.
func (e *Evaluator) drop(ctx context.Context, tr *track, reason ClearReason, res *result) {
	switch {
	case !tr.isActive():
		e.remove(tr)
	case tr.ending == "" || memoryOnly(reason):
		e.end(ctx, tr, reason, res)
	}
}

// reason bestimmt, warum ein Befund endet, dessen Grund nicht mehr gilt (Reihenfolge laut
// docs/contracts/core.md).
func (e *Evaluator) reason(g *inventory.Graph, s State, tr *track) ClearReason {
	id := tr.key.element
	el, ok := g.Element(id)
	if !ok {
		return ReasonDeleted
	}
	r, isRule := e.rules.byID[tr.key.rule]
	if tr.finding.Origin == OriginRule {
		switch {
		case !isRule:
			return ReasonRuleDeleted
		case !r.Enabled:
			return ReasonRuleDisabled
		}
	}
	if src, ok := g.Source(el.SourceID); ok && !src.Enabled {
		return ReasonSourceDisabled
	}
	switch {
	case !g.Monitored(id):
		return ReasonUnmonitored
	case !el.Present:
		return ReasonElementMissing
	case tr.finding.Origin != OriginRule:
		return ReasonResolved // Alarm bzw. Systemzustand ist weg
	}
	if t, ok := newTarget(s, id); ok {
		_, okFact := t.fact(r.Fact)
		okWhen := true
		if r.when != nil {
			_, okWhen = t.fact(r.when.fact)
		}
		if !okFact || !okWhen {
			return ReasonFactMissing
		}
	}
	return ReasonRuleChanged
}

// memoryOnly meldet Gründe, die der Store schon in derselben Transaktion geschrieben hat.
func memoryOnly(r ClearReason) bool {
	return r == ReasonDeleted || r == ReasonRuleDeleted || r == ReasonRuleDisabled
}

// prune räumt nach einer Änderung des Inventars auf: Alarme gelöschter Elemente, Fehler
// gelöschter Quellen und Regeln, deren Geltungsbereich gelöscht ist (in der Datenbank per
// CASCADE); deren Befunde beendet es im Speicher.
func (e *Evaluator) prune(g *inventory.Graph, res *result) {
	if g == e.graph {
		return
	}
	e.graph = g
	for id := range e.alarms {
		if _, ok := g.Element(id); !ok {
			delete(e.alarms, id)
		}
	}
	for id := range e.discoveryErr {
		if _, ok := g.Source(id); !ok {
			delete(e.discoveryErr, id)
		}
	}
	var gone []uuid.UUID
	for _, r := range e.rules.list {
		if _, ok := g.Element(r.ScopeElementID); r.ScopeElementID != uuid.Nil && !ok {
			gone = append(gone, r.ID)
		}
	}
	if len(gone) == 0 {
		return
	}
	e.rules = e.rules.without(gone...)
	for _, tr := range e.sortedTracks() {
		if slices.Contains(gone, tr.key.rule) {
			e.drop(context.Background(), tr, ReasonRuleDeleted, res)
		}
	}
}

// raise schreibt einen Befund. Scheitert das, bleibt er unsichtbar in Raising, und Tick
// wiederholt mit derselben ID.
func (e *Evaluator) raise(ctx context.Context, now time.Time, g *inventory.Graph, tr *track, res *result) {
	if tr.phase == phasePending {
		tr.phase = phaseRaising
		tr.finding.ID = e.newID()
		tr.finding.ElementID = tr.key.element
		tr.finding.ElementPath = g.Path(tr.key.element)
		tr.finding.RaisedAt = now
		tr.finding.CausedByID = e.causeOf(g, tr.finding)
	}
	f := tr.finding
	err := e.write(ctx, func(ctx context.Context) error { return e.store.RaiseFinding(ctx, f) })
	switch {
	case errors.Is(err, ErrAlreadyActive):
		// Meist hat ein früherer Versuch nach dem Timeout doch geschrieben.
		e.log.Warn("finding already active", "element", f.ElementID, "code", f.Code, "rule", f.RuleID)
	case err != nil:
		res.errs = append(res.errs, fmt.Errorf("raise %s on %q: %w", f.Code, f.ElementPath, err))
		return
	}
	tr.phase = phaseActive
	e.active[f.ID] = f
	e.healthDirty[f.ElementID] = true
	res.changes = append(res.changes, FindingChange{Raised: &f})
}

// end beendet einen aktiven Befund. Scheitert das Schreiben, bleibt er aktiv, und Tick
// wiederholt.
func (e *Evaluator) end(ctx context.Context, tr *track, reason ClearReason, res *result) {
	id := tr.finding.ID
	if !memoryOnly(reason) {
		err := e.write(ctx, func(ctx context.Context) error { return e.store.EndFinding(ctx, id, reason) })
		if err != nil && !errors.Is(err, ErrNotFound) {
			tr.ending = reason
			res.errs = append(res.errs, fmt.Errorf("end %s on %q: %w", tr.finding.Code, tr.finding.ElementPath, err))
			return
		}
	}
	e.remove(tr)
	delete(e.active, id)
	e.healthDirty[tr.key.element] = true
	res.changes = append(res.changes, FindingChange{Ended: id, Reason: reason})
}

func (e *Evaluator) write(ctx context.Context, f func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return f(ctx)
}

// live meldet, ob ein Element bewertet wird: Seine Quelle ist Live, oder es hat keine.
func (e *Evaluator) live(s State, id uuid.UUID) bool {
	el, ok := s.Graph().Element(id)
	return ok && (el.SourceID == uuid.Nil || s.Live(el.SourceID))
}

// holds liefert die Haltezeiten eines Befunds; ok ist false, wenn seine Regel fehlt.
func (e *Evaluator) holds(key findingKey) (hold, clearHold time.Duration, ok bool) {
	if key.rule == uuid.Nil {
		return 0, 0, true
	}
	r, ok := e.rules.byID[key.rule]
	if !ok {
		return 0, 0, false
	}
	return time.Duration(r.HoldSeconds) * time.Second, time.Duration(r.ClearHoldSeconds) * time.Second, true
}

func (e *Evaluator) track(key findingKey) *track { return e.tracks[key.element][key] }

func (e *Evaluator) put(tr *track) {
	m := e.tracks[tr.key.element]
	if m == nil {
		m = map[findingKey]*track{}
		e.tracks[tr.key.element] = m
	}
	m[tr.key] = tr
}

func (e *Evaluator) remove(tr *track) {
	m := e.tracks[tr.key.element]
	delete(m, tr.key)
	if len(m) == 0 {
		delete(e.tracks, tr.key.element)
	}
}

func (e *Evaluator) elementTracks(id uuid.UUID) []*track {
	m := e.tracks[id]
	return slices.SortedFunc(maps.Values(m), func(a, b *track) int { return compareKeys(a.key, b.key) })
}

func (e *Evaluator) sortedTracks() []*track {
	var all []*track
	for _, m := range e.tracks {
		all = slices.AppendSeq(all, maps.Values(m))
	}
	slices.SortFunc(all, func(a, b *track) int { return compareKeys(a.key, b.key) })
	return all
}

// ruleSnapshot hält die Regel im Befund fest, damit er nach ihrem Löschen lesbar bleibt.
type ruleSnapshot struct {
	ID               uuid.UUID       `json:"id"`
	Name             string          `json:"name"`
	Severity         Severity        `json:"severity"`
	Fact             string          `json:"fact"`
	Op               Op              `json:"op"`
	Value            json.RawMessage `json:"value"`
	When             *whenSnapshot   `json:"when,omitempty"`
	ClearValue       json.RawMessage `json:"clear_value,omitempty"`
	HoldSeconds      int             `json:"hold_seconds,omitempty"`
	ClearHoldSeconds int             `json:"clear_hold_seconds,omitempty"`
}

type whenSnapshot struct {
	Fact  string          `json:"fact"`
	Op    Op              `json:"op"`
	Value json.RawMessage `json:"value"`
}

// ruleFinding ist die Vorlage eines Regelbefunds mit dem gemessenen Wert.
func ruleFinding(r *rule, t target) Finding {
	snap := ruleSnapshot{
		ID: r.ID, Name: r.Name, Severity: r.Severity, Fact: r.Fact, Op: r.Op, Value: compact(r.Value),
		ClearValue: compact(r.ClearValue), HoldSeconds: r.HoldSeconds, ClearHoldSeconds: r.ClearHoldSeconds,
	}
	if w := r.When; w != nil {
		snap.When = &whenSnapshot{Fact: w.Fact, Op: w.Op, Value: compact(w.Value)}
	}
	details := map[string]any{"rule": snap}
	if v, ok := t.fact(r.Fact); ok {
		details["value"] = v
	}
	b, err := json.Marshal(details)
	if err != nil { // etwa NaN als Messwert
		delete(details, "value")
		b, _ = json.Marshal(details)
	}
	return Finding{
		Origin:   OriginRule,
		RuleID:   r.ID,
		Code:     CodeRuleViolated,
		Severity: r.Severity,
		Message:  fmt.Sprintf("%s %s %s", r.Fact, r.Op, compact(r.Value)),
		Details:  b,
	}
}

func compact(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return raw
	}
	return buf.Bytes()
}
