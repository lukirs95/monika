package auth

import (
	"strings"
	"sync"
	"time"
)

// Begrenzung der Fehlversuche (A5, M2).
const (
	throttleFree   = 4 // so viele Fehlversuche ohne Wartezeit
	throttleFirst  = 30 * time.Second
	throttleMax    = 15 * time.Minute
	throttleForget = time.Hour
)

// throttle zählt Fehlversuche pro (Benutzername, IP) im Speicher.
type throttle struct {
	mu      sync.Mutex
	entries map[throttleKey]*throttleEntry
	sweep   time.Time
}

type throttleKey struct{ user, ip string }

type throttleEntry struct {
	failures int
	last     time.Time
}

func newThrottle() *throttle { return &throttle{entries: map[throttleKey]*throttleEntry{}} }

// wait liefert, wie lange noch gewartet werden muss; 0 = Versuch erlaubt.
func (t *throttle) wait(user, ip string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.forget(now)
	e := t.entries[throttleKey{strings.ToLower(user), ip}]
	if e == nil || e.failures <= throttleFree {
		return 0
	}
	d := throttleFirst << (e.failures - throttleFree - 1)
	if d > throttleMax || d <= 0 {
		d = throttleMax
	}
	return max(0, e.last.Add(d).Sub(now))
}

func (t *throttle) fail(user, ip string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := throttleKey{strings.ToLower(user), ip}
	e := t.entries[k]
	if e == nil {
		e = &throttleEntry{}
		t.entries[k] = e
	}
	e.failures++
	e.last = now
}

func (t *throttle) succeed(user, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, throttleKey{strings.ToLower(user), ip})
}

// forget entfernt höchstens einmal pro Minute Einträge ohne Versuch seit einer Stunde; t.mu ist
// gesperrt.
func (t *throttle) forget(now time.Time) {
	if now.Sub(t.sweep) < time.Minute {
		return
	}
	t.sweep = now
	for k, e := range t.entries {
		if now.Sub(e.last) > throttleForget {
			delete(t.entries, k)
		}
	}
}
