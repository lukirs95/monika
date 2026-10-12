package core

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/core/state"
	"github.com/lukirs95/monika/sdk"
)

const (
	queueSize    = 1024 // Warteschlange für Do und Post
	maxPerRound  = 1024 // Einträge pro Runde, damit auch unter Last regelmäßig eine View erscheint
	tickInterval = time.Second
	pingInterval = 5 * time.Second // Datenbank-Wächter
	pingTimeout  = 2 * time.Second
	stopTimeout  = 3 * time.Second // Phase 4 des Herunterfahrens: Rest verarbeiten, letztes SaveHealth
)

// Pinger prüft, ob die Datenbank erreichbar ist (der Pool von PostgreSQL).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Config enthält, was der Core braucht; verdrahtet wird in cmd/monika.
type Config struct {
	Inventory  *inventory.Inventory
	Evaluator  *evaluation.Evaluator
	Supervisor Supervisor
	Secrets    Secrets
	DB         Pinger // nil = kein Datenbank-Wächter (Tests)
	Log        *slog.Logger
}

// Core ist die Schleife: Sie besitzt Inventar, Fakten, Lebenszeichen und Bewertung, verarbeitet
// Befehle, Nachrichten und Meldungen der Treiber in Runden und veröffentlicht nach jeder Runde
// eine neue View (docs/contracts/core.md).
type Core struct {
	inv     *inventory.Inventory
	ev      *evaluation.Evaluator
	sup     Supervisor
	secrets Secrets
	pinger  Pinger
	log     *slog.Logger
	now     func() time.Time
	tick    time.Duration
	ping    time.Duration

	queue chan entry
	done  chan struct{} // geschlossen, wenn Run zurückgekehrt ist
	hub   *hub
	view  atomic.Pointer[View]

	mbMu      sync.Mutex
	mailboxes map[uuid.UUID]*mailbox // aktuelle Instanz pro Quelle
	ready     []*mailbox
	signal    chan struct{}

	// Ab hier gehört alles der Schleife.
	facts     *state.Facts
	live      *state.Liveness
	version   uint64
	factsView map[uuid.UUID]sdk.Facts
	findings  cow[uuid.UUID, evaluation.Finding]
	runs      cow[uuid.UUID, action.Run]
	statuses  cow[uuid.UUID, SourceStatus]
	wasLive   map[uuid.UUID]bool
	db        DBStatus
	history   InfluxStatus

	pending         map[uuid.UUID]*retry          // gescheiterte Snapshots pro Quelle
	discoveryFailed map[uuid.UUID]bool            // source.discovery_failed ist gemeldet
	warned          map[uuid.UUID]map[string]bool // unbekannte external_ids, je Quelle bis zum nächsten Snapshot
}

// entry ist ein Eintrag der Warteschlange: ein Befehl mit Antwortkanal oder eine Nachricht.
type entry struct {
	at    time.Time
	cmd   Command
	reply chan error
	msg   Message
}

// New liefert den Core mit dem Stand von Inventar und Evaluator als erster View.
func New(cfg Config) *Core {
	c := &Core{
		inv: cfg.Inventory, ev: cfg.Evaluator, sup: cfg.Supervisor, secrets: cfg.Secrets, pinger: cfg.DB, log: cfg.Log,
		now:             time.Now,
		tick:            tickInterval,
		ping:            pingInterval,
		queue:           make(chan entry, queueSize),
		done:            make(chan struct{}),
		hub:             newHub(),
		mailboxes:       map[uuid.UUID]*mailbox{},
		signal:          make(chan struct{}, 1),
		facts:           state.NewFacts(),
		live:            state.NewLiveness(),
		factsView:       map[uuid.UUID]sdk.Facts{},
		findings:        cow[uuid.UUID, evaluation.Finding]{m: cfg.Evaluator.Findings()},
		wasLive:         map[uuid.UUID]bool{},
		db:              DBStatus{OK: true},
		pending:         map[uuid.UUID]*retry{},
		discoveryFailed: map[uuid.UUID]bool{},
		warned:          map[uuid.UUID]map[string]bool{},
	}
	c.view.Store(c.buildView(Stats{}))
	return c
}

// View liefert den zuletzt veröffentlichten Stand, ohne zu warten.
func (c *Core) View() *View { return c.view.Load() }

// Subscribe meldet einen Abonnenten an (docs/contracts/core.md, Abonnenten).
func (c *Core) Subscribe(buffer int) (<-chan Event, func()) { return c.hub.subscribe(buffer) }

// Post stellt eine Nachricht eines Dienstes in die Schleife; es blockiert nur, wenn die
// Warteschlange voll ist. Nachrichten des Supervisors zu einer Quelle gehen in das Postfach ihrer
// aktuellen Instanz, damit sie in Reihenfolge mit den Meldungen des Treibers verarbeitet werden;
// der Supervisor holt deshalb erst Reporter, meldet dann SourceStarted und startet dann Run.
func (c *Core) Post(m Message) {
	if id, ok := sourceOf(m); ok {
		c.mbMu.Lock()
		mb := c.mailboxes[id]
		c.mbMu.Unlock()
		if mb != nil {
			mb.put(item{kind: itemMessage, msg: m})
			return
		}
	}
	c.queue <- entry{at: c.now(), msg: m}
}

func sourceOf(m Message) (uuid.UUID, bool) {
	switch m := m.(type) {
	case SourceStarted:
		return m.SourceID, true
	case SourceStopped:
		return m.SourceID, true
	case SourceFailed:
		return m.SourceID, true
	case SourceStuck:
		return m.SourceID, true
	}
	return uuid.Nil, false
}

// Run ist die Schleife: Sie startet alle aktivierten Quellen und den Datenbank-Wächter und
// verarbeitet Runde für Runde. Mit ctx endet sie: Sie verarbeitet, was noch wartet, schreibt die
// letzte Health und kehrt zurück (Phase 4 des Herunterfahrens, höchstens stopTimeout).
func (c *Core) Run(ctx context.Context) error {
	defer close(c.done)
	var wg sync.WaitGroup
	defer wg.Wait()
	if c.pinger != nil {
		wg.Go(func() { c.watchDB(ctx) })
	}

	r := c.newRound()
	g := c.inv.Graph()
	for _, id := range slices.SortedFunc(maps.Keys(maps.Collect(g.Sources())), compareIDs) {
		if src, _ := g.Source(id); src.Enabled {
			c.startSource(r, src)
		}
	}
	c.publish(ctx, r)

	ticker := time.NewTicker(c.tick)
	defer ticker.Stop()
	for {
		r := c.newRound()
		select {
		case <-ctx.Done():
			c.stop(ctx)
			return nil
		case e := <-c.queue:
			c.handle(ctx, r, e)
		case <-c.signal:
			c.handleReady(ctx, r)
		case <-ticker.C:
			c.handleTick(ctx, r)
		}
		c.drain(ctx, r)
		c.publish(ctx, r)
	}
}

// round sammelt, was eine Runde geändert hat.
type round struct {
	now     time.Time
	lag     time.Duration
	events  []Event
	replies []reply
	health  map[uuid.UUID]bool
	facts   bool
}

type reply struct {
	ch  chan error
	err error
}

func (c *Core) newRound() *round { return &round{now: c.now(), health: map[uuid.UUID]bool{}} }

// drain arbeitet ab, was gerade wartet, höchstens maxPerRound Einträge.
func (c *Core) drain(ctx context.Context, r *round) {
	for range maxPerRound {
		select {
		case e := <-c.queue:
			c.handle(ctx, r, e)
		case <-c.signal:
			c.handleReady(ctx, r)
		default:
			return
		}
	}
}

func (c *Core) handle(ctx context.Context, r *round, e entry) {
	r.lag = max(r.lag, c.now().Sub(e.at))
	switch {
	case e.cmd != nil:
		r.replies = append(r.replies, reply{e.reply, c.handleCommand(ctx, r, e.cmd)})
	case e.msg != nil:
		c.handleMessage(ctx, r, e.msg)
	}
}

// stop verarbeitet nach dem Ende von ctx, was noch wartet, und schreibt die letzte Health.
func (c *Core) stop(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	r := c.newRound()
	c.drain(ctx, r)
	c.publish(ctx, r)
	if err := c.ev.Flush(ctx); err != nil {
		c.log.Error("final health not saved", "err", err)
	}
}

// watchDB pingt die Datenbank im Takt und meldet das Ergebnis (DBPing).
func (c *Core) watchDB(ctx context.Context) {
	ticker := time.NewTicker(c.ping)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pctx, cancel := context.WithTimeout(ctx, pingTimeout)
		err := c.pinger.Ping(pctx)
		cancel()
		select {
		case c.queue <- entry{at: c.now(), msg: DBPing{OK: err == nil, Err: err}}:
		case <-ctx.Done():
			return
		}
	}
}

// publish schließt eine Runde ab: Zustand der Quellen, Health, neue View, Ereignisse, Antworten.
func (c *Core) publish(ctx context.Context, r *round) {
	c.updateSources(ctx, r)
	ids := make([]uuid.UUID, 0, len(r.health))
	for id := range r.health {
		ids = append(ids, id)
	}
	if hs := c.ev.Health(c.state(r.now), ids); len(hs) > 0 {
		r.events = append(r.events, HealthChanged{Health: hs})
	}
	if r.facts {
		c.factsView = c.facts.Snapshot()
	}
	c.version++
	subs, dropped, events := c.hub.stats()
	v := c.buildView(Stats{LoopLag: r.lag, LoopQueue: len(c.queue), Subscribers: subs, SubscribersDropped: dropped, Events: events})
	for i, e := range r.events {
		r.events[i] = WithVersion(e, v.Version)
	}
	c.view.Store(v)
	c.hub.publish(r.events)
	for _, rp := range r.replies {
		rp.ch <- rp.err
	}
}

func (c *Core) buildView(stats Stats) *View {
	return &View{
		Version:  c.version,
		Graph:    c.inv.Graph(),
		Facts:    c.factsView,
		Health:   c.ev.Healths(),
		Findings: c.findings.publish(),
		Rules:    c.ev.Rules(),
		Runs:     c.runs.publish(),
		Sources:  c.statuses.publish(),
		DB:       c.db,
		History:  c.history,
		Stats:    stats,
	}
}

// applyFindings übernimmt Änderungen des Evaluators in die Befunde der View und in die Ereignisse.
// Fehler sind gescheiterte Schreibvorgänge: Die Datenbank gilt dann als nicht in Ordnung.
func (c *Core) applyFindings(r *round, changes []evaluation.FindingChange, err error) {
	for _, ch := range changes {
		switch {
		case ch.Raised != nil:
			c.findings.set(ch.Raised.ID, *ch.Raised)
			r.events = append(r.events, FindingRaised{Finding: *ch.Raised})
		case ch.Ended != uuid.Nil:
			f := c.findings.m[ch.Ended]
			f.ClearedAt = r.now
			c.findings.del(ch.Ended)
			r.events = append(r.events, FindingEnded{Finding: f, Reason: ch.Reason})
		case ch.CauseSet != uuid.Nil:
			f := c.findings.m[ch.CauseSet]
			f.CausedByID = ch.CausedBy
			c.findings.set(f.ID, f)
			r.events = append(r.events, FindingCauseChanged{FindingID: ch.CauseSet, CausedBy: ch.CausedBy})
		}
	}
	if err != nil {
		c.writeFailed(r, err)
	}
}

// writeFailed vermerkt einen gescheiterten Schreibvorgang; der nächste gelungene Ping des
// Datenbank-Wächters hebt das auf.
func (c *Core) writeFailed(r *round, err error) {
	c.log.Warn("database write failed", "err", err)
	if c.db.OK {
		c.db = DBStatus{Since: r.now}
	}
	c.db.Error = err.Error()
}

// cow ist eine Map, die nach dem Veröffentlichen nie mehr verändert wird: Die erste Änderung
// danach arbeitet auf einer Kopie.
type cow[K comparable, V any] struct {
	m     map[K]V
	owned bool
}

func (c *cow[K, V]) set(k K, v V) {
	c.own()
	c.m[k] = v
}

func (c *cow[K, V]) del(k K) {
	if _, ok := c.m[k]; ok {
		c.own()
		delete(c.m, k)
	}
}

func (c *cow[K, V]) own() {
	if !c.owned {
		c.m, c.owned = maps.Clone(c.m), true
		if c.m == nil {
			c.m = map[K]V{}
		}
	}
}

// publish gibt die Map zur Veröffentlichung frei; danach wird sie nicht mehr verändert.
func (c *cow[K, V]) publish() map[K]V {
	c.owned = false
	return c.m
}

// loopState ist der aktuelle Stand der Schleife für den Evaluator (evaluation.State).
type loopState struct {
	c   *Core
	now time.Time
}

func (c *Core) state(now time.Time) loopState { return loopState{c, now} }

func (s loopState) Graph() *inventory.Graph      { return s.c.inv.Graph() }
func (s loopState) Facts(id uuid.UUID) sdk.Facts { return s.c.facts.Element(id) }
func (s loopState) Live(id uuid.UUID) bool       { return s.c.live.Live(id, s.now) }
func (s loopState) Heartbeat(id uuid.UUID) time.Duration {
	st, _ := s.c.live.Status(id, s.now)
	return st.Heartbeat
}

// Unreachable liefert den Grund für details: no_heartbeat, disconnected, failed oder stuck, bei
// den letzten dreien mit dem Fehlertext.
func (s loopState) Unreachable(id uuid.UUID) (string, bool) {
	if !s.c.live.IsUnreachable(id, s.now) {
		return "", false
	}
	st, _ := s.c.live.Status(id, s.now)
	reason := "no_heartbeat"
	switch {
	case st.Phase == state.PhaseBackoff:
		reason = "failed"
	case st.Phase == state.PhaseStuck:
		reason = "stuck"
	case st.Err != "":
		reason = "disconnected"
	}
	if st.Err != "" {
		reason += ": " + st.Err
	}
	return reason, true
}
