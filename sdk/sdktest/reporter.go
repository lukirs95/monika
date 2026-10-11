// Package sdktest ist der Testbaukasten für Treiber und Core (docs/contracts/sdk.md): ein
// aufzeichnender Reporter, ein einfacher Scheduler, Conformance für die Regeln des SDK-Vertrags
// und ein programmierbarer Fake-Treiber, mit dem sich der Core ohne echte Geräte testen lässt.
package sdktest

import (
	"context"
	"sync"

	"github.com/lukirs95/monika/sdk"
)

// CallKind ist die Art einer Meldung an den Reporter.
type CallKind string

const (
	CallConnected    CallKind = "connected"
	CallDisconnected CallKind = "disconnected"
	CallSnapshot     CallKind = "snapshot"
	CallUpdate       CallKind = "update"
	CallHeartbeat    CallKind = "heartbeat"
)

// Call ist eine aufgezeichnete Meldung.
type Call struct {
	Kind     CallKind
	Err      error         // bei CallDisconnected
	Snapshot *sdk.Snapshot // bei CallSnapshot
	Update   *sdk.Update   // bei CallUpdate
}

// Reporter zeichnet alle Meldungen einer Source auf. Er ist für gleichzeitige Nutzung sicher.
type Reporter struct {
	mu      sync.Mutex
	calls   []Call
	changed chan struct{} // wird bei jeder Meldung geschlossen und ersetzt
}

var _ sdk.Reporter = (*Reporter)(nil)

// NewReporter liefert einen leeren Reporter.
func NewReporter() *Reporter { return &Reporter{changed: make(chan struct{})} }

func (r *Reporter) record(c Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *Reporter) Connected()             { r.record(Call{Kind: CallConnected}) }
func (r *Reporter) Disconnected(err error) { r.record(Call{Kind: CallDisconnected, Err: err}) }
func (r *Reporter) Snapshot(s sdk.Snapshot) {
	r.record(Call{Kind: CallSnapshot, Snapshot: &s})
}
func (r *Reporter) Update(u sdk.Update) { r.record(Call{Kind: CallUpdate, Update: &u}) }
func (r *Reporter) Heartbeat()          { r.record(Call{Kind: CallHeartbeat}) }

// Calls liefert eine Kopie aller bisherigen Meldungen.
func (r *Reporter) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

// Snapshots liefert alle gemeldeten Snapshots.
func (r *Reporter) Snapshots() []sdk.Snapshot {
	var out []sdk.Snapshot
	for _, c := range r.Calls() {
		if c.Snapshot != nil {
			out = append(out, *c.Snapshot)
		}
	}
	return out
}

// Updates liefert alle gemeldeten Updates.
func (r *Reporter) Updates() []sdk.Update {
	var out []sdk.Update
	for _, c := range r.Calls() {
		if c.Update != nil {
			out = append(out, *c.Update)
		}
	}
	return out
}

// WaitFor wartet, bis eine Meldung ab Position from pred erfüllt, und liefert sie mit ihrer
// Position. Endet ctx vorher, kommt ctx.Err() zurück.
func (r *Reporter) WaitFor(ctx context.Context, from int, pred func(Call) bool) (Call, int, error) {
	for {
		r.mu.Lock()
		for i := from; i < len(r.calls); i++ {
			if pred(r.calls[i]) {
				c := r.calls[i]
				r.mu.Unlock()
				return c, i, nil
			}
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return Call{}, -1, ctx.Err()
		}
	}
}

// Is liefert ein Prädikat für WaitFor, das Meldungen einer Art erkennt.
func Is(kind CallKind) func(Call) bool { return func(c Call) bool { return c.Kind == kind } }
