package core

import (
	"maps"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/sdk"
)

type itemKind int

const (
	itemConnected itemKind = iota
	itemDisconnected
	itemSnapshot
	itemUpdate
	itemHeartbeat
	itemMessage // Nachricht des Supervisors zu dieser Quelle
)

// item ist eine Meldung eines Treibers mit dem Zeitpunkt, zu dem er sie gemeldet hat.
type item struct {
	kind itemKind
	at   time.Time
	err  error
	snap sdk.Snapshot
	upd  sdk.Update
	msg  Message
}

// mailbox ist das Postfach einer laufenden Quelleninstanz: Der Treiber legt Meldungen ab, ohne je
// zu blockieren; die Schleife holt sie in Reihenfolge ab. Ein Update wird in ein direkt davor
// stehendes Update gemischt (neuester Wert pro Element und Fakt, neueste Alarmliste pro
// Element), ein Heartbeat in eine direkt davor stehende Meldung derselben Art bzw. ein Update;
// über Connected, Snapshot und Disconnected hinweg wird nie gemischt.
type mailbox struct {
	core   *Core
	source uuid.UUID

	mu     sync.Mutex
	items  []item
	queued bool // steht in Core.ready, bis die Schleife das Postfach geleert hat
	closed bool // eine neue Instanz hat begonnen bzw. die Quelle ist gelöscht: nichts mehr annehmen
}

var _ sdk.Reporter = (*mailbox)(nil)

// Reporter liefert das Postfach einer neuen Instanz der Quelle; der Supervisor ruft es bei jedem
// Start. Was die frühere Instanz bis dahin gemeldet hat, verarbeitet die Schleife noch in
// Reihenfolge; was sie danach meldet, verwirft ihr geschlossenes Postfach.
func (c *Core) Reporter(sourceID uuid.UUID) sdk.Reporter {
	mb := &mailbox{core: c, source: sourceID}
	c.mbMu.Lock()
	old := c.mailboxes[sourceID]
	c.mailboxes[sourceID] = mb
	c.mbMu.Unlock()
	old.close()
	return mb
}

// close lässt das Postfach nichts mehr annehmen.
func (m *mailbox) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
}

func (m *mailbox) Connected()              { m.put(item{kind: itemConnected}) }
func (m *mailbox) Disconnected(err error)  { m.put(item{kind: itemDisconnected, err: err}) }
func (m *mailbox) Snapshot(s sdk.Snapshot) { m.put(item{kind: itemSnapshot, snap: s}) }
func (m *mailbox) Update(u sdk.Update)     { m.put(item{kind: itemUpdate, upd: cloneUpdate(u)}) }
func (m *mailbox) Heartbeat()              { m.put(item{kind: itemHeartbeat}) }

func (m *mailbox) put(it item) {
	it.at = m.core.now()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	n := len(m.items)
	switch {
	case n > 0 && it.kind == itemUpdate && m.items[n-1].kind == itemUpdate:
		mergeUpdate(&m.items[n-1].upd, it.upd)
		m.items[n-1].at = it.at
	case n > 0 && it.kind == itemHeartbeat && (m.items[n-1].kind == itemHeartbeat || m.items[n-1].kind == itemUpdate):
		m.items[n-1].at = it.at
	default:
		m.items = append(m.items, it)
	}
	notify := !m.queued
	m.queued = true
	m.mu.Unlock()
	if notify {
		m.core.markReady(m)
	}
}

// take leert das Postfach.
func (m *mailbox) take() []item {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.items
	m.items, m.queued = nil, false
	return items
}

// markReady stellt das Postfach in die Liste der Schleife und weckt sie, ohne zu blockieren.
func (c *Core) markReady(m *mailbox) {
	c.mbMu.Lock()
	c.ready = append(c.ready, m)
	c.mbMu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// takeReady liefert die Postfächer mit Meldungen in der Reihenfolge, in der sie bereit wurden;
// das Postfach einer früheren Instanz steht damit vor dem der neuen.
func (c *Core) takeReady() []*mailbox {
	c.mbMu.Lock()
	defer c.mbMu.Unlock()
	out := c.ready
	c.ready = nil
	return out
}

// cloneUpdate kopiert die Maps, denn der Treiber darf seine nach dem Melden weiterverwenden.
func cloneUpdate(u sdk.Update) sdk.Update {
	out := sdk.Update{Facts: make(map[string]sdk.Facts, len(u.Facts)), Alarms: maps.Clone(u.Alarms)}
	for ext, f := range u.Facts {
		out.Facts[ext] = maps.Clone(f)
	}
	return out
}

// mergeUpdate mischt src in dst; beide gehören dem Postfach.
func mergeUpdate(dst *sdk.Update, src sdk.Update) {
	for ext, f := range src.Facts {
		if dst.Facts[ext] == nil {
			dst.Facts[ext] = f
			continue
		}
		maps.Copy(dst.Facts[ext], f)
	}
	if len(src.Alarms) > 0 && dst.Alarms == nil {
		dst.Alarms = map[string][]sdk.Alarm{}
	}
	maps.Copy(dst.Alarms, src.Alarms)
}
