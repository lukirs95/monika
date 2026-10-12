package core

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/core/state"
	"github.com/lukirs95/monika/sdk"
)

const maxRetryWait = time.Minute

// retry ist ein Snapshot, der nicht gespeichert werden konnte; die Schleife wiederholt ihn mit
// steigender Wartezeit, ohne das Gerät erneut abzufragen.
type retry struct {
	snap sdk.Snapshot
	at   time.Time // gemeldet
	wait time.Duration
	next time.Time
}

// handleReady holt die Meldungen aller Postfächer ab, die etwas enthalten.
func (c *Core) handleReady(ctx context.Context, r *round) {
	for _, mb := range c.takeReady() {
		for _, it := range mb.take() {
			r.lag = max(r.lag, c.now().Sub(it.at))
			c.handleItem(ctx, r, mb.source, it)
		}
	}
}

func (c *Core) handleItem(ctx context.Context, r *round, id uuid.UUID, it item) {
	if it.kind == itemMessage {
		c.handleMessage(ctx, r, it.msg)
		return
	}
	if _, ok := c.inv.Graph().Source(id); !ok {
		return // gelöscht
	}
	switch it.kind {
	case itemConnected:
		c.live.Connected(id, it.at)
	case itemDisconnected:
		c.log.Info("source disconnected", "source", id, "err", it.err)
		c.live.Disconnected(id, it.err, it.at)
	case itemHeartbeat:
		c.live.Touch(id, it.at)
	case itemSnapshot:
		c.live.Touch(id, it.at)
		c.applySnapshot(ctx, r, id, it.snap, it.at)
	case itemUpdate:
		c.live.Touch(id, it.at)
		c.applyUpdate(ctx, r, id, it.upd)
	}
}

// applySnapshot übernimmt den vollständigen Stand einer Quelle (docs/contracts/core.md, Ablauf
// Snapshot). Scheitert das Speichern, merkt sich die Schleife den Snapshot und wiederholt.
func (c *Core) applySnapshot(ctx context.Context, r *round, id uuid.UUID, s sdk.Snapshot, at time.Time) {
	st := c.state(r.now)
	diff, err := c.inv.ApplySnapshot(ctx, id, s)
	if err != nil {
		c.failSnapshot(ctx, r, id, s, at, err)
		return
	}
	delete(c.pending, id)
	if c.discoveryFailed[id] {
		delete(c.discoveryFailed, id)
		changes, err := c.ev.Discovery(ctx, r.now, id, nil, st)
		c.applyFindings(r, changes, err)
	}
	c.inventoryChanged(r, diff)

	facts, unknown := c.inv.FactsByID(id, s.Facts)
	delete(c.warned, id)
	c.warnUnknown(id, unknown)
	changed := c.facts.Drop(diff.Missing)
	maps.Copy(changed, c.facts.Replace(id, facts))
	c.factsChanged(r, changed)
	c.live.SnapshotApplied(id, at)

	alarms := c.alarmsByID(id, s.Alarms)
	changes, err := c.ev.Alarms(ctx, r.now, id, alarms, true, st)
	c.applyFindings(r, changes, err)
	// Mit dem Snapshot ist die Quelle Live: alle ihre Elemente neu bewerten, auch verschwundene.
	ids := c.sourceElements(id)
	changes, err = c.ev.Evaluate(ctx, r.now, ids, st)
	c.applyFindings(r, changes, err)
	for _, e := range ids {
		r.health[e] = true
	}
	c.wasLive[id] = c.live.Live(id, r.now)
}

func (c *Core) failSnapshot(ctx context.Context, r *round, id uuid.UUID, s sdk.Snapshot, at time.Time, err error) {
	c.log.Warn("snapshot not saved", "source", id, "err", err)
	p := c.pending[id]
	if p == nil {
		p = &retry{wait: time.Second}
	} else {
		p.wait = min(2*p.wait, maxRetryWait)
	}
	p.snap, p.at, p.next = s, at, r.now.Add(p.wait)
	c.pending[id] = p
	c.discoveryFailed[id] = true
	changes, e := c.ev.Discovery(ctx, r.now, id, err, c.state(r.now))
	c.applyFindings(r, changes, e)
}

// applyUpdate übernimmt geänderte Fakten und Alarme. Vor dem ersten Snapshot nach Connected wird
// es verworfen; ist die Quelle nicht Live, wird es übernommen, aber nicht bewertet. Wartet ein
// gescheiterter Snapshot, wird es in ihn gemischt.
func (c *Core) applyUpdate(ctx context.Context, r *round, id uuid.UUID, u sdk.Update) {
	if p := c.pending[id]; p != nil {
		mergeIntoSnapshot(&p.snap, u)
		return
	}
	if !c.live.HasSnapshot(id) {
		return
	}
	st := c.state(r.now)
	facts, unknown := c.inv.FactsByID(id, u.Facts)
	c.warnUnknown(id, unknown)
	changed := c.facts.Merge(id, facts)
	c.factsChanged(r, changed)
	if alarms := c.alarmsByID(id, u.Alarms); len(alarms) > 0 {
		changes, err := c.ev.Alarms(ctx, r.now, id, alarms, false, st)
		c.applyFindings(r, changes, err)
	}
	if len(changed) > 0 && c.live.Live(id, r.now) {
		changes, err := c.ev.Evaluate(ctx, r.now, slices.Collect(maps.Keys(changed)), st)
		c.applyFindings(r, changes, err)
	}
	for e := range changed {
		r.health[e] = true
	}
}

// handleTick: abgelaufene Wartezeiten gescheiterter Snapshots, Haltezeiten, Lebenszeichen.
func (c *Core) handleTick(ctx context.Context, r *round) {
	for _, id := range slices.SortedFunc(maps.Keys(c.pending), compareIDs) {
		if p := c.pending[id]; !r.now.Before(p.next) {
			c.applySnapshot(ctx, r, id, p.snap, p.at)
		}
	}
	changes, err := c.ev.Tick(ctx, r.now, c.state(r.now))
	c.applyFindings(r, changes, err)
}

// handleMessage verarbeitet Nachrichten der Dienste.
func (c *Core) handleMessage(_ context.Context, r *round, m Message) {
	known := func(id uuid.UUID) bool {
		_, ok := c.inv.Graph().Source(id)
		return ok
	}
	switch m := m.(type) {
	case SourceStarted:
		if known(m.SourceID) {
			c.live.Started(m.SourceID, m.Heartbeat, r.now)
		}
	case SourceStopped:
		if st, ok := c.live.Status(m.SourceID, r.now); ok && st.Phase == state.PhaseStopping {
			c.live.Remove(m.SourceID)
		}
	case SourceFailed:
		if m.Panic {
			c.log.Error("source panicked", "source", m.SourceID, "err", m.Err)
		} else {
			c.log.Warn("source failed", "source", m.SourceID, "err", m.Err)
		}
		if known(m.SourceID) {
			c.live.Failed(m.SourceID, m.Err, r.now)
		}
	case SourceStuck:
		c.log.Error("source does not stop", "source", m.SourceID)
		if known(m.SourceID) {
			c.live.Stuck(m.SourceID, r.now)
		}
	case RunChanged:
		if m.Run.Finished() {
			c.runs.del(m.Run.ID)
		} else {
			c.runs.set(m.Run.ID, m.Run)
		}
		r.events = append(r.events, RunChanged{Run: m.Run})
	case InfluxStatus:
		c.history = m
	case DBPing:
		switch {
		case m.OK && !c.db.OK:
			c.db = DBStatus{OK: true, Since: r.now}
		case !m.OK && c.db.OK:
			c.db = DBStatus{Since: r.now, Error: errText(m.Err)}
		case !m.OK:
			c.db.Error = errText(m.Err)
		}
	}
}

// updateSources setzt den Zustand aller Quellen für die View. Wird eine Quelle Live oder ist sie
// es nicht mehr, ändert sich die Health aller ihrer Elemente; wird sie Live, werden sie bewertet.
func (c *Core) updateSources(ctx context.Context, r *round) {
	g := c.inv.Graph()
	for id, src := range g.Sources() {
		st := c.sourceStatus(src, r.now)
		old, ok := c.statuses.m[id]
		if st.Since = old.Since; !ok || old.State != st.State {
			st.Since = r.now
		}
		if !ok || old != st {
			c.statuses.set(id, st)
		}
		if !ok || old.State != st.State || old.Heartbeat != st.Heartbeat || old.Error != st.Error {
			r.events = append(r.events, SourceStatusChanged{SourceID: id, Status: st})
		}
		live := c.live.Live(id, r.now)
		if live == c.wasLive[id] {
			continue
		}
		c.wasLive[id] = live
		ids := c.sourceElements(id)
		for _, e := range ids {
			r.health[e] = true
		}
		if live {
			changes, err := c.ev.Evaluate(ctx, r.now, ids, c.state(r.now))
			c.applyFindings(r, changes, err)
		}
	}
	for id := range c.statuses.m {
		if _, ok := g.Source(id); !ok {
			c.statuses.del(id)
			delete(c.wasLive, id)
		}
	}
}

func (c *Core) sourceStatus(src inventory.Source, now time.Time) SourceStatus {
	if !src.Enabled {
		return SourceStatus{State: StateDisabled}
	}
	st, ok := c.live.Status(src.ID, now)
	if !ok {
		return SourceStatus{State: StateStarting}
	}
	return SourceStatus{State: SourceState(st.Phase), Heartbeat: st.Heartbeat, LastSeen: st.LastSeen, Error: st.Err}
}

// sourceElements liefert alle Elemente einer Quelle, sortiert.
func (c *Core) sourceElements(id uuid.UUID) []uuid.UUID {
	var ids []uuid.UUID
	for eid, e := range c.inv.Graph().All() {
		if e.SourceID == id {
			ids = append(ids, eid)
		}
	}
	slices.SortFunc(ids, compareIDs)
	return ids
}

func (c *Core) inventoryChanged(r *round, d inventory.Diff) {
	if len(d.Added)+len(d.Changed)+len(d.Missing)+len(d.Returned)+len(d.Deleted) == 0 && !d.LinksChanged {
		return
	}
	r.events = append(r.events, ElementsChanged{
		Added: d.Added, Changed: d.Changed, Missing: d.Missing, Returned: d.Returned, Deleted: d.Deleted,
		LinksChanged: d.LinksChanged,
	})
}

func (c *Core) factsChanged(r *round, changed state.Changed) {
	if len(changed) == 0 {
		return
	}
	r.facts = true
	facts := make(map[uuid.UUID]sdk.Facts, len(changed))
	for id := range changed {
		facts[id] = c.facts.Element(id)
	}
	r.events = append(r.events, FactsChanged{Changed: changed, Facts: facts})
}

// alarmsByID übersetzt die Alarme einer Meldung von external_ids in Element-IDs.
func (c *Core) alarmsByID(source uuid.UUID, alarms map[string][]sdk.Alarm) map[uuid.UUID][]sdk.Alarm {
	out := make(map[uuid.UUID][]sdk.Alarm, len(alarms))
	var unknown []string
	for ext, list := range alarms {
		if id, ok := c.inv.ByExternalID(source, ext); ok {
			out[id] = list
		} else {
			unknown = append(unknown, ext)
		}
	}
	slices.Sort(unknown)
	c.warnUnknown(source, unknown)
	return out
}

// warnUnknown loggt Meldungen an unbekannten external_ids einmal pro Quelle und external_id bis
// zum nächsten Snapshot.
func (c *Core) warnUnknown(source uuid.UUID, exts []string) {
	for _, ext := range exts {
		if c.warned[source][ext] {
			continue
		}
		if c.warned[source] == nil {
			c.warned[source] = map[string]bool{}
		}
		c.warned[source][ext] = true
		c.log.Warn("report for unknown element dropped", "source", source, "external_id", ext)
	}
}

// mergeIntoSnapshot mischt ein Update in einen gemerkten Snapshot; der Snapshot gehört der
// Schleife.
func mergeIntoSnapshot(s *sdk.Snapshot, u sdk.Update) {
	if s.Facts == nil {
		s.Facts = map[string]sdk.Facts{}
	}
	for ext, f := range u.Facts {
		next := maps.Clone(s.Facts[ext])
		if next == nil {
			next = sdk.Facts{}
		}
		maps.Copy(next, f)
		s.Facts[ext] = next
	}
	if len(u.Alarms) > 0 && s.Alarms == nil {
		s.Alarms = map[string][]sdk.Alarm{}
	}
	maps.Copy(s.Alarms, u.Alarms)
}

func compareIDs(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) }

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
