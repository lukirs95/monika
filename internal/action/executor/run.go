package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/sdk"
)

// Zeiten aus docs/contracts/services.md und flows.md.
const (
	executeTimeout = 30 * time.Second
	stopWait       = 5 * time.Second // F8: sent wartet beim Stopp gemeinsam höchstens so lange
)

// ErrFinished bedeutet, dass ein Lauf schon im Endzustand ist (API: 409 run_finished).
var ErrFinished = errors.New("run already finished")

const reasonStopped = "monika stopped"

// Config enthält, was der Executor braucht; verdrahtet wird in cmd/monika.
type Config struct {
	Core    Core
	Sources Sources
	Store   Store
	Drivers func(name string) (sdk.Driver, bool) // meist sdk.Lookup
	Log     *slog.Logger
}

// Executor führt Läufe aus: pro Element nacheinander (FIFO), über die Quelle, die das Element
// besitzt; Erfolg ist der beobachtete Zustand (Prüfbedingung).
type Executor struct {
	cfg     Config
	planner planner
	now     func() time.Time

	executeTimeout, stopWait time.Duration

	mu      sync.Mutex
	ctx     context.Context // von Run; nil = Run läuft noch nicht
	stopped bool
	runs    map[uuid.UUID]*runState       // Läufe, die noch nicht im Endzustand sind
	queues  map[uuid.UUID][]uuid.UUID     // Element → wartende Läufe; vorhanden = Goroutine läuft
	waiters map[uuid.UUID][]chan struct{} // Element → Läufe, die auf ihre Prüfbedingung warten
	results map[uuid.UUID]action.Run      // beendete Schritt-Läufe offener Presets (Result)
	workers sync.WaitGroup
}

type runState struct {
	run      action.Run
	children int  // übergeordneter Lauf: Kinder, die noch nicht im Endzustand sind
	cancel   bool // übergeordneter Lauf wurde abgebrochen
	worst    map[action.Status]int
}

// New liefert einen Executor; Submit gelingt erst, wenn Run läuft.
func New(cfg Config) *Executor {
	return &Executor{
		cfg:            cfg,
		planner:        planner{drivers: cfg.Drivers, newID: func() uuid.UUID { return uuid.Must(uuid.NewV7()) }},
		now:            time.Now,
		executeTimeout: executeTimeout, stopWait: stopWait,
		runs:    map[uuid.UUID]*runState{},
		queues:  map[uuid.UUID][]uuid.UUID{},
		waiters: map[uuid.UUID][]chan struct{}{},
		results: map[uuid.UUID]action.Run{},
	}
}

// Submit löst den Auftrag auf, legt die Läufe in einer Transaktion an und reiht die Element-Läufe
// ein; es liefert sofort die ID des obersten Laufs.
func (e *Executor) Submit(ctx context.Context, r Request) (uuid.UUID, error) {
	e.mu.Lock()
	ready := e.ctx != nil && !e.stopped
	e.mu.Unlock()
	if !ready {
		return uuid.Nil, ErrStopped
	}
	now := e.now()
	p, err := e.planner.plan(e.cfg.Core.View(), r, now)
	if err != nil {
		return uuid.Nil, err
	}
	if err := e.cfg.Store.CreateRuns(ctx, p.runs); err != nil {
		return uuid.Nil, fmt.Errorf("create runs: %w", err)
	}
	e.mu.Lock()
	for _, run := range p.runs {
		if !run.Finished() {
			e.runs[run.ID] = &runState{run: run, worst: map[action.Status]int{}}
		}
	}
	if parent := e.runs[p.runs[0].ID]; parent != nil {
		parent.children = len(p.runs) - 1
	}
	e.mu.Unlock()
	for _, run := range p.runs {
		e.cfg.Core.Post(core.RunChanged{Run: run})
	}
	// Schon beim Anlegen beendete Kinder zählen sofort; danach die übrigen einreihen.
	for _, run := range p.runs[1:] {
		if run.Finished() {
			e.childDone(ctx, run)
		}
	}
	for _, run := range p.runs {
		if run.ElementID != uuid.Nil && run.SourceID != uuid.Nil && !run.Finished() {
			e.enqueue(run.ElementID, run.ID)
		}
	}
	return p.runs[0].ID, nil
}

// Cancel bricht Läufe ab: wartende (pending) enden als cancelled; ein übergeordneter Lauf gibt
// das an alle seine offenen Nachfahren weiter und endet, wenn sie enden, selbst als cancelled.
// Gesendete laufen zu Ende.
func (e *Executor) Cancel(ctx context.Context, ids []uuid.UUID) error {
	var errs []error
	for _, id := range ids {
		e.mu.Lock()
		rs, ok := e.runs[id]
		e.mu.Unlock()
		if !ok {
			errs = append(errs, fmt.Errorf("cancel %s: %w", id, ErrFinished))
			continue
		}
		e.cancel(ctx, rs.run.ID)
	}
	return errors.Join(errs...)
}

func (e *Executor) cancel(ctx context.Context, id uuid.UUID) {
	e.mu.Lock()
	rs, ok := e.runs[id]
	if !ok {
		e.mu.Unlock()
		return
	}
	var children []uuid.UUID
	if rs.children > 0 {
		rs.cancel = true
		for cid, c := range e.runs {
			if c.run.ParentID == id {
				children = append(children, cid)
			}
		}
	}
	pending := rs.run.Status == action.StatusPending && rs.children == 0 && rs.run.ElementID != uuid.Nil
	e.mu.Unlock()
	for _, cid := range children {
		e.cancel(ctx, cid)
	}
	if pending {
		e.finish(ctx, id, action.StatusPending, action.StatusCancelled, nil)
	}
}

// Run verteilt die Änderungen der Fakten an wartende Prüfbedingungen, bis ctx endet. Dann
// startet es keine Läufe mehr: pending wird failed, sent wartet höchstens 5 s auf die Antwort,
// accepted wird unconfirmed (F8).
func (e *Executor) Run(ctx context.Context) error {
	events, unsubscribe := e.cfg.Core.Subscribe(4096)
	e.mu.Lock()
	e.ctx = ctx
	e.mu.Unlock()
	for running := true; running; {
		select {
		case <-ctx.Done():
			running = false
		case ev, ok := <-events:
			if !ok { // abgehängt: neu anmelden, alle prüfen erneut gegen die View
				events, unsubscribe = e.cfg.Core.Subscribe(4096)
				e.wakeAll()
				continue
			}
			if fc, ok := ev.(core.FactsChanged); ok {
				for id := range fc.Changed {
					e.wake(id)
				}
			}
		}
	}
	unsubscribe()
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()
	e.wakeAll()
	done := make(chan struct{})
	go func() {
		e.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(e.stopWait):
		e.abandon()
	}
	return nil
}

// abandon beendet nach der Frist des Stopps alle Läufe, die noch auf eine Antwort warten.
func (e *Executor) abandon() {
	e.mu.Lock()
	var sent []uuid.UUID
	for id, rs := range e.runs {
		if rs.run.Status == action.StatusSent {
			sent = append(sent, id)
		}
	}
	e.mu.Unlock()
	for _, id := range sent {
		e.finish(context.Background(), id, action.StatusSent, action.StatusFailed, reason(reasonStopped))
	}
}

func (e *Executor) wake(element uuid.UUID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ch := range e.waiters[element] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (e *Executor) wakeAll() {
	e.mu.Lock()
	ids := make([]uuid.UUID, 0, len(e.waiters))
	for id := range e.waiters {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.wake(id)
	}
}

// enqueue reiht einen Lauf in die Warteschlange seines Elements; ihre Goroutine entsteht bei
// Bedarf und endet, wenn die Warteschlange leer ist.
func (e *Executor) enqueue(element, id uuid.UUID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	q, busy := e.queues[element]
	e.queues[element] = append(q, id)
	if !busy {
		ctx := e.ctx
		e.workers.Go(func() { e.work(ctx, element) })
	}
}

func (e *Executor) work(ctx context.Context, element uuid.UUID) {
	for {
		e.mu.Lock()
		q := e.queues[element]
		if len(q) == 0 {
			delete(e.queues, element)
			e.mu.Unlock()
			return
		}
		id := q[0]
		e.queues[element] = q[1:]
		rs, ok := e.runs[id]
		claimed := ok && rs.run.Status == action.StatusPending && !e.stopped
		stopped := ok && rs.run.Status == action.StatusPending && e.stopped
		var run action.Run
		if claimed {
			rs.run.Status, rs.run.SentAt = action.StatusSent, e.now()
			run = rs.run
		}
		e.mu.Unlock()
		switch {
		case stopped:
			e.finish(ctx, id, action.StatusPending, action.StatusFailed, reason(reasonStopped))
		case claimed:
			e.execute(ctx, run)
		}
	}
}

// execute sendet einen Lauf an die Quelle und wartet bei angenommenem Befehl auf die
// Prüfbedingung.
func (e *Executor) execute(ctx context.Context, run action.Run) {
	e.update(ctx, run)
	el, ok := e.cfg.Core.View().Graph.Element(run.ElementID)
	switch {
	case !ok:
		e.finish(ctx, run.ID, action.StatusSent, action.StatusFailed, reason("element not found"))
		return
	case !el.Present:
		e.finish(ctx, run.ID, action.StatusSent, action.StatusFailed, reason("element not present"))
		return
	}
	// Die Antwort darf auch beim Stopp noch kommen; Run wartet darauf höchstens stopWait.
	xctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.executeTimeout)
	res, err := e.cfg.Sources.Execute(xctx, run.SourceID, sdk.Command{ExternalID: el.ExternalID, Action: run.Action, Params: run.Params})
	cancel()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		e.finish(ctx, run.ID, action.StatusSent, action.StatusFailed, reason("timeout"))
		return
	case err != nil:
		e.finish(ctx, run.ID, action.StatusSent, action.StatusFailed, reason(err.Error()))
		return
	case res.Status == sdk.Rejected:
		e.finish(ctx, run.ID, action.StatusSent, action.StatusRejected, response(res))
		return
	}
	if run.Verify == nil {
		e.finish(ctx, run.ID, action.StatusSent, action.StatusAccepted, response(res))
		return
	}
	run, ok = e.transition(run.ID, action.StatusSent, action.StatusAccepted, response(res), false)
	if !ok {
		return
	}
	e.update(ctx, run)
	if e.verify(ctx, run) {
		e.finish(ctx, run.ID, action.StatusAccepted, action.StatusConfirmed, nil)
	} else {
		e.finish(ctx, run.ID, action.StatusAccepted, action.StatusUnconfirmed, nil)
	}
}

// verify wartet, bis alle Bedingungen erfüllt sind, längstens bis zum Timeout; beim Stopp gibt es
// sofort auf.
func (e *Executor) verify(ctx context.Context, run action.Run) bool {
	wake := make(chan struct{}, 1)
	e.mu.Lock()
	e.waiters[run.ElementID] = append(e.waiters[run.ElementID], wake)
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		ws := e.waiters[run.ElementID]
		for i, w := range ws {
			if w == wake {
				ws = append(ws[:i], ws[i+1:]...)
				break
			}
		}
		if len(ws) == 0 {
			delete(e.waiters, run.ElementID)
		} else {
			e.waiters[run.ElementID] = ws
		}
	}()
	deadline := time.NewTimer(run.Verify.Timeout)
	defer deadline.Stop()
	for {
		if e.conditionsMet(run) {
			return true
		}
		select {
		case <-wake:
			e.mu.Lock()
			stopped := e.stopped
			e.mu.Unlock()
			if stopped {
				return false
			}
		case <-deadline.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

func (e *Executor) conditionsMet(run action.Run) bool {
	facts := e.cfg.Core.View().Facts[run.ElementID]
	for _, c := range run.Verify.Conditions {
		ok, err := c.Check(facts)
		if err != nil {
			e.cfg.Log.Warn("invalid verify condition", "run", run.ID, "fact", c.Fact, "err", err)
		}
		if !ok {
			return false
		}
	}
	return true
}

// transition ändert den Status eines Laufs, wenn er noch im erwarteten ist; final beendet ihn.
func (e *Executor) transition(id uuid.UUID, from, to action.Status, resp json.RawMessage, final bool) (action.Run, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rs, ok := e.runs[id]
	if !ok || rs.run.Status != from {
		return action.Run{}, false
	}
	now := e.now()
	rs.run.Status = to
	if from == action.StatusSent {
		rs.run.RespondedAt = now
	}
	if resp != nil {
		rs.run.Response = resp
	}
	if final {
		rs.run.FinishedAt = now
		delete(e.runs, id)
		if parent := e.runs[rs.run.ParentID]; parent != nil && parent.run.Action == ActionPreset {
			e.results[id] = rs.run
		}
	}
	return rs.run, true
}

// finish bringt einen Lauf in einen Endzustand, wenn er noch im erwarteten Status ist, und
// rechnet ihn beim übergeordneten Lauf an.
func (e *Executor) finish(ctx context.Context, id uuid.UUID, from, to action.Status, resp json.RawMessage) {
	run, ok := e.transition(id, from, to, resp, true)
	if !ok {
		return
	}
	e.update(ctx, run)
	e.childDone(ctx, run)
}

// childDone rechnet einen beendeten Lauf beim übergeordneten an; enden alle Kinder, endet er mit
// dem schlechtesten Status (cancelled zählt nicht) bzw. als cancelled, wenn er abgebrochen wurde.
// Den Wurzel-Lauf eines Presets beendet der Preset-Runner.
func (e *Executor) childDone(ctx context.Context, child action.Run) {
	e.mu.Lock()
	rs, ok := e.runs[child.ParentID]
	if !ok || rs.run.Action == ActionPreset {
		e.mu.Unlock()
		return
	}
	rs.children--
	rs.worst[child.Status]++
	if rs.children > 0 {
		e.mu.Unlock()
		return
	}
	status := worst(rs.worst)
	if rs.cancel {
		status = action.StatusCancelled
	}
	var sum summary
	_ = json.Unmarshal(rs.run.Response, &sum)
	sum.Statuses = rs.worst
	b, _ := json.Marshal(sum)
	from := rs.run.Status
	e.mu.Unlock()
	e.finish(ctx, child.ParentID, from, status, b)
}

// worst ist der schlechteste Status: failed > rejected > unconfirmed > accepted > confirmed;
// cancelled zählt nicht, sind alle abgebrochen, ist es cancelled.
func worst(counts map[action.Status]int) action.Status {
	for _, s := range []action.Status{action.StatusFailed, action.StatusRejected, action.StatusUnconfirmed, action.StatusAccepted, action.StatusConfirmed} {
		if counts[s] > 0 {
			return s
		}
	}
	return action.StatusCancelled
}

// update schreibt den Lauf und meldet ihn dem Core; scheitert das Schreiben, wird es geloggt
// (offene Läufe schließt der nächste Start ab, F2).
func (e *Executor) update(ctx context.Context, run action.Run) {
	if err := e.cfg.Store.UpdateRun(context.WithoutCancel(ctx), run); err != nil {
		e.cfg.Log.Error("run not saved", "run", run.ID, "status", run.Status, "err", err)
	}
	e.cfg.Core.Post(core.RunChanged{Run: run})
}

func reason(s string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"error": s})
	return b
}

func response(res sdk.Result) json.RawMessage {
	out := map[string]any{}
	if res.Detail != "" {
		out["detail"] = res.Detail
	}
	if len(res.RejectedFields) > 0 {
		out["rejected_fields"] = res.RejectedFields
	}
	if len(res.Response) > 0 {
		out["response"] = res.Response
	}
	if len(out) == 0 {
		return nil
	}
	b, _ := json.Marshal(out)
	return b
}

// Target ist ein Ziel, wie Submit es auflösen würde.
type Target struct {
	ElementID uuid.UUID
	Critical  bool
}

// Targets löst einen Auftrag auf, ohne etwas anzulegen; der Preset-Runner friert damit die Ziele
// ein und prüft an genau ihnen die Rechte.
func (e *Executor) Targets(v *core.View, r Request) ([]Target, error) {
	p, err := e.planner.plan(v, r, e.now())
	if err != nil {
		return nil, err
	}
	runs := p.runs
	if len(runs) > 1 || runs[0].ElementID != r.ElementID || r.Targets != nil {
		runs = runs[1:] // übergeordneter Lauf
	}
	out := make([]Target, 0, len(runs))
	for _, run := range runs {
		out = append(out, Target{ElementID: run.ElementID, Critical: run.Critical})
	}
	return out, nil
}

// ErrNotPresetRoot bedeutet, dass Finish auf einen Lauf zielt, der kein Wurzel-Lauf eines Presets
// ist.
var ErrNotPresetRoot = errors.New("not the root run of a preset")

// Finish beendet den Wurzel-Lauf eines Presets; den führt der Preset-Runner, schreiben darf ihn nur
// der Executor.
func (e *Executor) Finish(ctx context.Context, id uuid.UUID, status action.Status, resp json.RawMessage) error {
	e.mu.Lock()
	rs, ok := e.runs[id]
	isRoot := ok && rs.run.Action == ActionPreset
	for cid, r := range e.results {
		if isRoot && r.ParentID == id {
			delete(e.results, cid)
		}
	}
	e.mu.Unlock()
	switch {
	case !ok:
		return fmt.Errorf("finish %s: %w", id, ErrFinished)
	case !isRoot:
		return fmt.Errorf("finish %s: %w", id, ErrNotPresetRoot)
	}
	run, ok := e.transition(id, action.StatusPending, status, resp, true)
	if ok {
		e.update(ctx, run)
	}
	return nil
}

// Result liefert einen beendeten Schritt-Lauf eines noch offenen Presets; der Preset-Runner fragt
// danach, wenn ihm Ereignisse entgangen sind.
func (e *Executor) Result(id uuid.UUID) (action.Run, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.results[id]
	return r, ok
}
