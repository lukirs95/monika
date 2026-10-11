package influx

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/lukirs95/monika/internal/core/coretest"
	"github.com/lukirs95/monika/sdk/vocab"
)

// BenchmarkTakt: einen Takt bilden und in Stücken komprimieren, wie der Schreiber es jede
// Sekunde tut; gzip-KiB ist, was davon eine Minute lang im Puffer läge.
func BenchmarkTakt(b *testing.B) {
	for _, n := range coretest.Sizes {
		b.Run(fmt.Sprintf("devices=%d", n), func(b *testing.B) {
			world := coretest.NewWorld(n)
			w := New(Config{Core: &fakeCore{view: world.View}, Field: vocab.Lookup, Log: slog.New(slog.DiscardHandler)})
			var size int
			for b.Loop() {
				tk, err := w.build(time.Now(), own{})
				if err != nil {
					b.Fatal(err)
				}
				size = 0
				for _, c := range tk.chunks {
					size += len(c)
				}
			}
			b.ReportMetric(float64(w.lastSize)/1024, "raw-KiB")
			b.ReportMetric(float64(size)/1024, "gzip-KiB")
		})
	}
}
