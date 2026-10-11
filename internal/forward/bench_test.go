package forward

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/coretest"
	"github.com/lukirs95/monika/internal/core/evaluation"
)

// BenchmarkDispatch: ein Befund-Ereignis an 10 Ziele mit Geltungsbereich (Gruppen) filtern und
// einreihen, ohne Versand.
func BenchmarkDispatch(b *testing.B) {
	for _, n := range coretest.Sizes {
		b.Run(fmt.Sprintf("devices=%d", n), func(b *testing.B) {
			w := coretest.NewWorld(n)
			fc := &fakeCore{events: make(chan core.Event)}
			fc.view.Store(w.View)
			f := New(Config{Core: fc, Log: slog.New(slog.DiscardHandler)})
			for i := range 10 {
				scope := w.All
				if i < len(w.Groups) {
					scope = w.Groups[i]
				}
				f.add(Target{ID: uuid.New(), Name: "t", Kind: KindWebhook, Enabled: true, Config: json.RawMessage(`{"url":"https://h"}`),
					Events: []string{EventFindingRaised, EventFindingCleared}, ScopeElementID: scope})
			}
			port := w.Devices[len(w.Devices)-1].Ports[5]
			i := 0
			for b.Loop() {
				ev := core.Event(core.FindingRaised{Finding: evaluation.Finding{ID: uuid.New(), ElementID: port, Severity: evaluation.SeverityError}})
				if i%2 == 1 {
					ev = core.FindingEnded{Finding: evaluation.Finding{ID: uuid.New(), ElementID: port, Severity: evaluation.SeverityError}, Reason: evaluation.ReasonResolved}
				}
				f.dispatch(ev)
				for _, ts := range f.targets {
					ts.queue = ts.queue[:0] // ohne Versand
				}
				i++
			}
		})
	}
}
