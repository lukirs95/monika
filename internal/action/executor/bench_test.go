package executor

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core/coretest"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// BenchmarkPlanCollective: eine Sammelaktion start auf die Gruppe über allen auflösen, ein Kind
// pro Port (96 pro Gerät).
func BenchmarkPlanCollective(b *testing.B) {
	for _, n := range coretest.Sizes {
		b.Run(fmt.Sprintf("devices=%d", n), func(b *testing.B) {
			w := coretest.NewWorld(n)
			p := planner{
				drivers: func(string) (sdk.Driver, bool) {
					return sdk.Driver{Name: coretest.Driver, Actions: []sdk.ActionDef{{Name: vocab.ActionStart, Verify: []sdk.Condition{{Fact: vocab.StateRunning, Op: "eq", Value: "true"}}}}}, true
				},
				newID: func() uuid.UUID { return uuid.Must(uuid.NewV7()) },
			}
			var runs int
			for b.Loop() {
				plan, err := p.plan(w.View, Request{ElementID: w.All, Action: vocab.ActionStart}, time.Now())
				if err != nil {
					b.Fatal(err)
				}
				runs = len(plan.runs)
			}
			b.ReportMetric(float64(runs), "runs")
		})
	}
}
