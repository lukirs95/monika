package core

import (
	"sync"
)

// hub verteilt Ereignisse an Abonnenten. Die Schleife wartet nie auf einen Abonnenten: Ist sein
// Puffer voll, hängt sie ihn ab und schließt seinen Kanal; er meldet sich neu an und liest die
// View (docs/contracts/core.md, Abonnenten).
type hub struct {
	mu      sync.Mutex
	subs    map[*subscriber]struct{}
	dropped uint64
	events  uint64
}

type subscriber struct {
	ch     chan Event
	closed bool
}

func newHub() *hub { return &hub{subs: map[*subscriber]struct{}{}} }

// subscribe meldet einen Abonnenten an; cancel meldet ihn ab und darf mehrfach gerufen werden.
func (h *hub) subscribe(buffer int) (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, buffer)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s.ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.close(s)
	}
}

// publish verteilt die Ereignisse einer Runde an alle Abonnenten.
func (h *hub) publish(events []Event) {
	if len(events) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events += uint64(len(events))
	for s := range h.subs {
		for _, e := range events {
			select {
			case s.ch <- e:
				continue
			default:
			}
			h.close(s)
			h.dropped++
			break
		}
	}
}

// close schließt den Kanal eines Abonnenten genau einmal; h.mu ist gesperrt.
func (h *hub) close(s *subscriber) {
	if s.closed {
		return
	}
	s.closed = true
	delete(h.subs, s)
	close(s.ch)
}

// stats liefert Abonnenten, abgehängte Abonnenten und verteilte Ereignisse.
func (h *hub) stats() (subscribers int, dropped, events uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs), h.dropped, h.events
}
