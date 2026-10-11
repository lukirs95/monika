package sdk

import "time"

// Counter berechnet die Rate eines Zählers pro Sekunde. Ein Counter pro Zähler und Element;
// nicht für gleichzeitige Aufrufe.
//
// Ist Width (32 oder 64) gesetzt, wird ein kleinerer Wert als Überlauf gerechnet, sonst als
// Reset. Differenzen rechnet der Counter ganzzahlig ohne Vorzeichen; float64 ist erst die Rate.
type Counter struct {
	Width int

	last   uint64
	lastAt time.Time
	seen   bool
}

// Rate nimmt den Rohwert raw zum Messzeitpunkt at (Zeitstempel des Geräts, sonst der Abfrage)
// und liefert die Rate pro Sekunde. ok ist false beim ersten Wert und wenn at nicht nach dem
// vorigen Messzeitpunkt liegt; nach einem Reset ist die Rate 0.
func (c *Counter) Rate(raw int64, at time.Time) (rate float64, ok bool) {
	v := uint64(raw)
	prev, prevAt, seen := c.last, c.lastAt, c.seen
	c.last, c.lastAt, c.seen = v, at, true
	if !seen {
		return 0, false
	}
	dt := at.Sub(prevAt).Seconds()
	if dt <= 0 {
		return 0, false
	}
	switch {
	case v >= prev:
		return float64(v-prev) / dt, true
	case c.Width == 32:
		return float64(v+(1<<32)-prev) / dt, true
	case c.Width == 64:
		return float64(v-prev) / dt, true // modulo 2⁶⁴ durch den Überlauf von uint64
	default:
		return 0, true // Reset
	}
}
