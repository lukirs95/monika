package influx

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lukirs95/monika/internal/core/coretest"
	"github.com/lukirs95/monika/sdk/vocab"
)

// BenchmarkTakt: alle Zeilen eines Takts bilden und gzip-komprimieren, wie der Schreiber es jede
// Sekunde tut.
func BenchmarkTakt(b *testing.B) {
	for _, n := range coretest.Sizes {
		b.Run(fmt.Sprintf("devices=%d", n), func(b *testing.B) {
			w := coretest.NewWorld(n)
			var size, count int
			for b.Loop() {
				ls := lines(w.View, time.Now(), vocab.Lookup, own{}, nil)
				var buf bytes.Buffer
				zw := gzip.NewWriter(&buf)
				_, _ = zw.Write([]byte(strings.Join(ls, "\n")))
				_ = zw.Close()
				size, count = buf.Len(), len(ls)
			}
			b.ReportMetric(float64(count), "lines")
			b.ReportMetric(float64(size)/1024, "gzip-KiB")
		})
	}
}
