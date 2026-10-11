package forward

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
)

// Zeiten und Grenzen aus docs/contracts/services.md und flows.md (F5).
const (
	queueSize   = 1000
	resultEvery = 10 * time.Second
	stopWait    = 5 * time.Second // Phase 5 des Herunterfahrens: Rest senden
)

var retryWaits = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}

// Core ist, was die Weiterleitung vom Core braucht.
type Core interface {
	View() *core.View
	Subscribe(buffer int) (<-chan core.Event, func())
}

// SecretValues liefert den Klartext eines Secrets (secret.Service).
type SecretValues interface {
	Value(ctx context.Context, id uuid.UUID) (json.RawMessage, error)
}

// Config enthält, was die Weiterleitung braucht; verdrahtet wird in cmd/monika.
type Config struct {
	Store     Store
	Core      Core
	Secrets   SecretValues
	PublicURL *url.URL
	Log       *slog.Logger
}

// Forwarder leitet Ereignisse an syslog und webhook weiter (best effort): gefiltert nach Typ,
// Schwere und Geltungsbereich, pro Ziel in eigener Warteschlange mit wenigen Wiederholungen.
type Forwarder struct {
	cfg    Config
	sender sender
	waits  []time.Duration
	now    func() time.Time

	mu       sync.Mutex
	ctx      context.Context // von Run
	draining chan struct{}   // geschlossen beim Herunterfahren
	abandon  chan struct{}   // geschlossen, wenn die Frist des Herunterfahrens abgelaufen ist
	targets  map[uuid.UUID]*targetState
	results  map[uuid.UUID]Result // noch nicht geschrieben
	workers  sync.WaitGroup
}

type targetState struct {
	target  Target
	queue   []message
	signal  chan struct{}
	removed chan struct{}      // geschlossen beim Löschen
	started bool               // Goroutine läuft
	open    map[uuid.UUID]bool // aktive Befunde, deren raised an dieses Ziel ging
}

// New liefert eine Weiterleitung; sie arbeitet, sobald Run läuft.
func New(cfg Config) *Forwarder {
	return &Forwarder{cfg: cfg, sender: newSender(), waits: retryWaits, now: time.Now,
		draining: make(chan struct{}), abandon: make(chan struct{}), targets: map[uuid.UUID]*targetState{}, results: map[uuid.UUID]Result{}}
}

// ListTargets liefert die Ziele mit dem Ergebnis des letzten Versands.
func (f *Forwarder) ListTargets(ctx context.Context) ([]Target, error) {
	return f.cfg.Store.ListTargets(ctx)
}

// CreateTarget prüft und speichert ein Ziel; die Weiterleitung arbeitet sofort mit ihm.
func (f *Forwarder) CreateTarget(ctx context.Context, actor uuid.UUID, t Target) error {
	if err := validate(t); err != nil {
		return err
	}
	if err := f.cfg.Store.CreateTarget(ctx, actor, t); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.add(t)
	return nil
}

// UpdateTarget prüft und ändert ein Ziel; seine Warteschlange bleibt.
func (f *Forwarder) UpdateTarget(ctx context.Context, actor uuid.UUID, t Target) error {
	if err := validate(t); err != nil {
		return err
	}
	if err := f.cfg.Store.UpdateTarget(ctx, actor, t); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ts := f.targets[t.ID]; ts != nil {
		ts.target = t
	} else {
		f.add(t)
	}
	return nil
}

// DeleteTarget löscht ein Ziel samt seiner Warteschlange.
func (f *Forwarder) DeleteTarget(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	if err := f.cfg.Store.DeleteTarget(ctx, actor, id); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ts := f.targets[id]; ts != nil {
		close(ts.removed)
		delete(f.targets, id)
		delete(f.results, id)
	}
	return nil
}

// add nimmt ein Ziel auf und startet seine Goroutine, wenn Run läuft; ein bekanntes Ziel wird
// nur aktualisiert. f.mu ist gesperrt.
func (f *Forwarder) add(t Target) {
	if ts := f.targets[t.ID]; ts != nil {
		ts.target = t
		return
	}
	ts := &targetState{target: t, signal: make(chan struct{}, 1), removed: make(chan struct{}), open: map[uuid.UUID]bool{}}
	f.targets[t.ID] = ts
	if f.ctx != nil {
		f.start(ts)
	}
}

// start startet die Goroutine eines Ziels; f.mu ist gesperrt.
func (f *Forwarder) start(ts *targetState) {
	ts.started = true
	f.workers.Go(func() { f.work(ts) })
}

// Run lädt die Ziele und leitet Ereignisse weiter, bis ctx endet; dann sendet es, was noch
// wartet, ohne Wiederholungen und höchstens 5 s, und schreibt die letzten Ergebnisse.
func (f *Forwarder) Run(ctx context.Context) error {
	targets, err := f.cfg.Store.ListTargets(ctx)
	if err != nil {
		return fmt.Errorf("list forward targets: %w", err)
	}
	events, unsubscribe := f.cfg.Core.Subscribe(1 << 16)
	f.mu.Lock()
	for _, t := range targets {
		f.add(t)
	}
	f.ctx = ctx
	for _, ts := range f.targets { // auch Ziele, die vor Run angelegt wurden
		if !ts.started {
			f.start(ts)
		}
	}
	f.mu.Unlock()
	ticker := time.NewTicker(resultEvery)
	defer ticker.Stop()
	for running := true; running; {
		select {
		case <-ctx.Done():
			running = false
		case <-ticker.C:
			f.flushResults(ctx)
		case ev, ok := <-events:
			if !ok {
				f.cfg.Log.Error("forwarding fell behind, events lost")
				events, unsubscribe = f.cfg.Core.Subscribe(1 << 16)
				continue
			}
			f.dispatch(ev)
		}
	}
	unsubscribe()
	close(f.draining)
	done := make(chan struct{})
	go func() {
		f.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopWait):
		close(f.abandon)
		f.cfg.Log.Warn("forwarding stopped with events left")
	}
	f.flushResults(context.WithoutCancel(ctx))
	return nil
}

// dispatch bildet aus einem Ereignis des Core die Nachricht und reiht sie bei allen passenden
// Zielen ein.
func (f *Forwarder) dispatch(ev core.Event) {
	now := f.now()
	var m message
	switch e := ev.(type) {
	case core.FindingRaised:
		m = findingMessage(EventFindingRaised, now, e.Finding, "", f.cfg.PublicURL)
	case core.FindingEnded:
		name := EventFindingWithdrawn
		if e.Reason == evaluation.ReasonResolved {
			name = EventFindingCleared
		}
		m = findingMessage(name, now, e.Finding, e.Reason, f.cfg.PublicURL)
	case core.FindingAcked:
		m = findingMessage(EventFindingAcked, now, e.Finding, "", f.cfg.PublicURL)
	case core.RunChanged:
		if !e.Run.Finished() || e.Run.ParentID != uuid.Nil {
			return // nur beendete Läufe ohne übergeordneten Lauf
		}
		m = runMessage(now, e.Run, f.cfg.PublicURL)
	default:
		return
	}
	v := f.cfg.Core.View()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ts := range f.targets {
		if f.matches(v, ts, m) {
			f.enqueue(ts, m)
		}
	}
}

// matches wendet die Filter eines Ziels an. Ging ein raised an das Ziel, gehen auch Ende und
// Quittung dorthin, selbst wenn das Element inzwischen gelöscht ist, damit sich der Alarm beim
// Empfänger schließt.
func (f *Forwarder) matches(v *core.View, ts *targetState, m message) bool {
	t := ts.target
	subscribed := t.Enabled && slices.Contains(t.Events, m.Event)
	var elementID uuid.UUID
	if m.Element != nil {
		elementID = m.Element.ID
	}
	inScope := t.ScopeElementID == uuid.Nil || (elementID != uuid.Nil && v.InScope(t.ScopeElementID, elementID))
	if m.Finding == nil {
		return subscribed && inScope
	}
	id := m.findID
	wasOpen := ts.open[id]
	if m.Event == EventFindingCleared || m.Event == EventFindingWithdrawn {
		delete(ts.open, id)
	}
	if !subscribed || rank(m.severity) < rank(t.MinSeverity) {
		return false
	}
	switch m.Event {
	case EventFindingRaised:
		if inScope && tracksEnd(t) {
			ts.open[id] = true
		}
		return inScope
	default:
		return inScope || wasOpen
	}
}

func tracksEnd(t Target) bool {
	return slices.ContainsFunc(t.Events, func(e string) bool {
		return e == EventFindingCleared || e == EventFindingWithdrawn || e == EventFindingAcked
	})
}

func rank(s evaluation.Severity) int {
	switch s {
	case evaluation.SeverityWarning:
		return 1
	case evaluation.SeverityError:
		return 2
	}
	return 0
}

// enqueue reiht ein; bei vollem Puffer fallen die ältesten weg. f.mu ist gesperrt.
func (f *Forwarder) enqueue(ts *targetState, m message) {
	ts.queue = append(ts.queue, m)
	if over := len(ts.queue) - queueSize; over > 0 {
		ts.queue = ts.queue[over:]
		f.cfg.Log.Warn("forward queue full, oldest events dropped", "target", ts.target.Name, "dropped", over)
	}
	select {
	case ts.signal <- struct{}{}:
	default:
	}
}

// work sendet die Warteschlange eines Ziels nacheinander, bis es gelöscht wird oder die
// Weiterleitung beim Herunterfahren alles gesendet hat.
func (f *Forwarder) work(ts *targetState) {
	for {
		select {
		case <-f.abandon:
			return
		default:
		}
		f.mu.Lock()
		var m message
		pending := len(ts.queue) > 0
		if pending {
			m = ts.queue[0]
			ts.queue = ts.queue[1:]
		}
		t := ts.target
		f.mu.Unlock()
		if pending {
			f.deliver(ts, t, m)
			continue
		}
		select {
		case <-ts.signal:
		case <-ts.removed:
			return
		case <-f.draining:
			return // Warteschlange leer
		}
	}
}

// deliver sendet mit Wiederholungen (1 s, 5 s, 30 s); beim Herunterfahren ohne.
func (f *Forwarder) deliver(ts *targetState, t Target, m message) {
	var err error
	for attempt := 0; ; attempt++ {
		err = f.send(t, m)
		if err == nil || attempt == len(f.waits) {
			break
		}
		select {
		case <-time.After(f.waits[attempt]):
		case <-ts.removed:
			return
		case <-f.draining:
			attempt = len(f.waits) - 1 // noch ein letzter Versuch
		}
	}
	r := Result{TargetID: t.ID, At: f.now()}
	if err != nil {
		r.Error = err.Error()
		f.cfg.Log.Warn("event not forwarded", "target", t.Name, "event", m.Event, "err", err)
	}
	f.mu.Lock()
	if f.targets[t.ID] == ts {
		f.results[t.ID] = r
	}
	f.mu.Unlock()
}

func (f *Forwarder) send(t Target, m message) error {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	var secret map[string]any
	if t.SecretID != uuid.Nil {
		raw, err := f.cfg.Secrets.Value(ctx, t.SecretID)
		if err != nil {
			return fmt.Errorf("secret: %w", err)
		}
		if err := json.Unmarshal(raw, &secret); err != nil {
			return fmt.Errorf("secret is not a JSON object")
		}
	}
	return f.sender.send(ctx, t, m, secret)
}

// flushResults schreibt die Ergebnisse seit dem letzten Mal gesammelt.
func (f *Forwarder) flushResults(ctx context.Context) {
	f.mu.Lock()
	rs := make([]Result, 0, len(f.results))
	for _, r := range f.results {
		rs = append(rs, r)
	}
	f.results = map[uuid.UUID]Result{}
	f.mu.Unlock()
	if len(rs) == 0 {
		return
	}
	if err := f.cfg.Store.SetTargetResults(ctx, rs); err != nil {
		f.cfg.Log.Warn("forward results not saved", "err", err)
	}
}
