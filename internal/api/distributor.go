package api

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/core"
)

// Verteiler (api.md, Verteiler): ein Abo am Core für alle Browser-Ströme. Er leitet nur weiter;
// filtern und Nutzdaten bauen die Ströme selbst (V1).

const (
	subscribeBuffer = 1 << 12 // Puffer des Abos am Core
	ringSize        = 100     // letzte Ereignisse ohne facts_changed für die Wiederaufnahme
	authzEvery      = time.Second
)

// errNoStream: Der Verteiler läuft (noch) nicht; die API antwortet mit 503.
var errNoStream = errors.New("event distributor not running")

// EventSource ist, was der Verteiler vom Core braucht.
type EventSource interface {
	View() *core.View
	Subscribe(buffer int) (<-chan core.Event, func())
}

// Distributor verteilt die Ereignisse des Core an die Ströme.
type Distributor struct {
	core         EventSource
	authzVersion func() uint64

	mu       sync.Mutex
	running  bool
	streams  map[uuid.UUID]*stream
	version  uint64       // Version des zuletzt verteilten Ereignisses
	ring     []core.Event // höchstens ringSize, das älteste zuerst
	ringFrom uint64       // der Ring enthält alle Ereignisse ab dieser Version
}

// NewDistributor liefert einen Verteiler; Ströme gibt es erst, wenn Run läuft.
func NewDistributor(c EventSource, authzVersion func() uint64) *Distributor {
	return &Distributor{core: c, authzVersion: authzVersion, streams: map[uuid.UUID]*stream{}}
}

// stream ist ein Ereignisstrom eines Browserfensters.
type stream struct {
	id        uuid.UUID
	principal auth.Principal
	*queue
	closed chan struct{} // der Verteiler ist beendet

	mu      sync.Mutex
	scopes  map[uuid.UUID]bool // beobachtete Geltungsbereiche (watch)
	watched map[uuid.UUID]bool // daraus aufgelöste, sichtbare Elemente
}

func (st *stream) watching(id uuid.UUID) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.watched[id]
}

// Run verteilt die Ereignisse, bis ctx endet; dann schließt es alle Ströme. Wird es vom Core
// abgehängt (C6), meldet es sich neu an, und alle Ströme laden neu (resync).
func (d *Distributor) Run(ctx context.Context) error {
	events, cancel := d.subscribe()
	defer func() { cancel() }()
	authz := d.authzVersion()
	ticker := time.NewTicker(authzEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			d.stop()
			return nil
		case e, ok := <-events:
			if !ok {
				cancel()
				events, cancel = d.subscribe()
				continue
			}
			d.dispatch(e)
		case <-ticker.C:
			// Rechte geändert: Jeder Strom filtert nach einem anderen Stand und lädt neu.
			if v := d.authzVersion(); v != authz {
				authz = v
				d.resyncAll()
			}
		}
	}
}

// subscribe meldet sich am Core an und beginnt neu: Ring leer, alle Ströme laden neu. Erst
// anmelden, dann die View lesen; was davor lag, steht im Stand.
func (d *Distributor) subscribe() (<-chan core.Event, func()) {
	events, cancel := d.core.Subscribe(subscribeBuffer)
	var version uint64
	if v := d.core.View(); v != nil {
		version = v.Version
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	wasRunning := d.running
	d.running, d.version, d.ring, d.ringFrom = true, version, nil, version+1
	if wasRunning {
		for _, st := range d.streams {
			st.requestResync()
		}
	}
	return events, cancel
}

// dispatch verteilt ein Ereignis an alle Ströme und merkt es im Ring.
func (d *Distributor) dispatch(e core.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.version = max(d.version, e.Version())
	if _, facts := e.(core.FactsChanged); !facts {
		if len(d.ring) == ringSize {
			d.ringFrom = max(d.ringFrom, d.ring[0].Version()+1)
			d.ring = d.ring[1:]
		}
		d.ring = append(d.ring, e)
	}
	for _, st := range d.streams {
		st.push(e, st.watching)
	}
}

func (d *Distributor) resyncAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, st := range d.streams {
		st.requestResync()
	}
}

func (d *Distributor) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running = false
	for id, st := range d.streams {
		close(st.closed)
		delete(d.streams, id)
	}
}

// open meldet einen Strom an und liefert die Version für hello: Alle Ereignisse danach bekommt
// der Strom. Mit lastEventID (SSE Last-Event-ID) bekommt er zusätzlich alle Ereignisse ab
// einschließlich dieser Version aus dem Ring; liegt sie nicht vollständig darin, lädt er neu.
func (d *Distributor) open(p auth.Principal, lastEventID string) (*stream, uint64, error) {
	st := &stream{id: newID(), principal: p, queue: newQueue(), closed: make(chan struct{}),
		scopes: map[uuid.UUID]bool{}, watched: map[uuid.UUID]bool{}}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return nil, 0, errNoStream
	}
	if lastEventID != "" {
		from, err := strconv.ParseUint(lastEventID, 10, 64)
		switch {
		case err != nil, from < d.ringFrom, from > d.version+1:
			st.requestResync()
		default:
			for _, e := range d.ring {
				if e.Version() >= from {
					st.push(e, st.watching)
				}
			}
		}
	}
	d.streams[st.id] = st
	return st, d.version, nil
}

// close meldet einen Strom ab.
func (d *Distributor) close(st *stream) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.streams, st.id)
}

// stream liefert einen offenen Strom.
func (d *Distributor) stream(id uuid.UUID) (*stream, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.streams[id]
	return st, ok
}

// Streams ist die Zahl offener Ströme (Kennzahl api_streams).
func (d *Distributor) Streams() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.streams)
}
