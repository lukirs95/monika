// Package influx schreibt Fakten, Health, Rollups und eigene Kennzahlen im festen Takt als Line
// Protocol nach InfluxDB (docs/contracts/influx.md).
package influx

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Vocabulary liefert das Feld eines Fakt-Schlüssels (vocab.Lookup).
type Vocabulary func(key string) (vocab.Field, bool)

// Gauges sind Kennzahlen anderer Dienste für das Measurement monika (etwa die offenen
// SSE-Ströme der API).
type Gauges func() map[string]int64

// own sind die Kennzahlen des Schreibers selbst.
type own struct {
	buffered, dropped int64
}

// encoder baut die Zeilen eines Takts direkt in einen Puffer. Die Reihenfolge der Elemente und
// ihre Tags hält er pro Graph zwischen den Takten, die Felder des Vokabulars pro Schlüssel; er
// gehört der Goroutine, die die Takte bildet.
type encoder struct {
	field  Vocabulary
	gauges Gauges

	graph   *inventory.Graph
	ids     []uuid.UUID
	tags    map[uuid.UUID]elementTags
	fields  map[string]fieldInfo
	scratch []factValue
}

type elementTags struct {
	facts, health, rollup string // ",tag=wert…", leere Tags weggelassen, escapt
}

type fieldInfo struct {
	ok                bool
	measurement, name string // escapt
	rateOf            string
}

type factValue struct {
	measurement, name string
	value             any
}

func newEncoder(field Vocabulary, gauges Gauges) *encoder {
	return &encoder{field: field, gauges: gauges, fields: map[string]fieldInfo{}}
}

// encode hängt alle Zeilen eines Takts mit dem Zeitstempel t an buf an und liefert die Zahl der
// Zeilen.
func (e *encoder) encode(buf []byte, v *core.View, t time.Time, me own) ([]byte, int) {
	if v.Graph != e.graph {
		e.reset(v.Graph)
	}
	ts := strconv.AppendInt(nil, t.UnixMilli(), 10)
	n := 0
	for _, id := range e.ids {
		el, _ := v.Graph.Element(id)
		tags := e.tagsOf(v.Graph, el)
		if el.SourceID != uuid.Nil && v.Sources[el.SourceID].State == core.StateLive {
			var k int
			buf, k = e.facts(buf, v.Facts[id], tags.facts, ts)
			n += k
		}
		h, ok := v.Health[id]
		if !ok {
			continue
		}
		if level, ok := healthLevel(h.Health); ok {
			buf = append(buf, "health"...)
			buf = append(buf, tags.health...)
			buf = append(buf, " level="...)
			buf = append(buf, level...)
			buf = endLine(buf, ts)
			n++
		}
		if r := h.Rollup; r != nil {
			buf = append(buf, "rollup"...)
			buf = append(buf, tags.rollup...)
			buf = appendInt(buf, " ok=", r.OK)
			buf = appendInt(buf, ",warning=", r.Warning)
			buf = appendInt(buf, ",error=", r.Error)
			buf = appendInt(buf, ",unknown=", r.Unknown)
			buf = appendInt(buf, ",unchecked=", r.Unchecked)
			buf = appendInt(buf, ",unmonitored=", r.Unmonitored)
			buf = appendInt(buf, ",missing=", r.Missing)
			buf = endLine(buf, ts)
			n++
		}
	}
	return e.monika(buf, v, me, ts), n + 1
}

// reset übernimmt einen neuen Graph: Reihenfolge der Elemente neu, Tags verwerfen.
func (e *encoder) reset(g *inventory.Graph) {
	e.graph = g
	e.ids = e.ids[:0]
	for id := range g.All() {
		e.ids = append(e.ids, id)
	}
	slices.SortFunc(e.ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	e.tags = make(map[uuid.UUID]elementTags, len(e.ids))
}

func (e *encoder) tagsOf(g *inventory.Graph, el inventory.Element) elementTags {
	if t, ok := e.tags[el.ID]; ok {
		return t
	}
	src, _ := g.Source(el.SourceID)
	id, kind := el.ID.String(), string(el.Kind)
	t := elementTags{
		facts: tags(tagSet{{"element_id", id}, {"device_id", nonNil(el.DeviceID)}, {"source_id", nonNil(el.SourceID)},
			{"driver", src.Driver}, {"kind", kind}, {"transport", el.Transport}}),
		health: tags(tagSet{{"element_id", id}, {"device_id", nonNil(el.DeviceID)}, {"kind", kind}}),
		rollup: tags(tagSet{{"element_id", id}, {"kind", kind}}),
	}
	e.tags[el.ID] = t
	return t
}

func (e *encoder) fieldOf(key string) fieldInfo {
	if fi, ok := e.fields[key]; ok {
		return fi
	}
	f, ok := e.field(key)
	fi := fieldInfo{ok: ok && f.Historized, measurement: escapeMeasurement(f.Measurement), name: escapeKey(f.Name), rateOf: f.RateOf}
	e.fields[key] = fi
	return fi
}

// facts schreibt pro Measurement einen Punkt mit allen historisierten Feldern; eine Rate nur,
// wenn das Element ihren Zähler nicht meldet. Rohe Fakten nie.
func (e *encoder) facts(buf []byte, facts map[string]any, tags string, ts []byte) ([]byte, int) {
	e.scratch = e.scratch[:0]
	for key, value := range facts {
		fi := e.fieldOf(key)
		if !fi.ok || !validValue(value) {
			continue
		}
		if _, counter := facts[fi.rateOf]; fi.rateOf != "" && counter {
			continue
		}
		e.scratch = append(e.scratch, factValue{fi.measurement, fi.name, value})
	}
	slices.SortFunc(e.scratch, func(a, b factValue) int {
		return cmp.Or(strings.Compare(a.measurement, b.measurement), strings.Compare(a.name, b.name))
	})
	n := 0
	for i, f := range e.scratch {
		if i == 0 || f.measurement != e.scratch[i-1].measurement {
			if i > 0 {
				buf = endLine(buf, ts)
			}
			buf = append(buf, f.measurement...)
			buf = append(buf, tags...)
			buf = append(buf, ' ')
			n++
		} else {
			buf = append(buf, ',')
		}
		buf = append(buf, f.name...)
		buf = append(buf, '=')
		buf = appendValue(buf, f.value)
	}
	if n > 0 {
		buf = endLine(buf, ts)
	}
	return buf, n
}

func (e *encoder) monika(buf []byte, v *core.View, me own, ts []byte) []byte {
	live, notLive := 0, 0
	for _, s := range v.Sources {
		switch s.State {
		case core.StateLive:
			live++
		case core.StateDisabled:
		default:
			notLive++
		}
	}
	st := v.Stats
	fields := []string{
		intField("loop_lag_ms", st.LoopLag.Milliseconds()), intField("loop_queue", st.LoopQueue),
		intField("sources_live", live), intField("sources_not_live", notLive),
		intField("subscribers", st.Subscribers), intField("subscribers_dropped", st.SubscribersDropped),
		intField("events", st.Events), "db_ok=" + strconv.FormatBool(v.DB.OK),
		intField("influx_buffered", me.buffered), intField("influx_dropped", me.dropped),
	}
	if e.gauges != nil {
		for name, value := range e.gauges() {
			fields = append(fields, intField(escapeKey(name), value))
		}
	}
	slices.Sort(fields)
	buf = append(buf, vocab.MeasurementMonika+" "...)
	buf = append(buf, strings.Join(fields, ",")...)
	return endLine(buf, ts)
}

func endLine(buf, ts []byte) []byte {
	buf = append(buf, ' ')
	buf = append(buf, ts...)
	return append(buf, '\n')
}

func appendInt(buf []byte, prefix string, v int) []byte {
	buf = append(buf, prefix...)
	buf = strconv.AppendInt(buf, int64(v), 10)
	return append(buf, 'i')
}

// healthLevel: 0 ok, 1 warning, 2 error, 3 unknown; ohne Health kein Punkt.
func healthLevel(h evaluation.Health) (string, bool) {
	switch h {
	case evaluation.HealthOK:
		return "0i", true
	case evaluation.HealthWarning:
		return "1i", true
	case evaluation.HealthError:
		return "2i", true
	case evaluation.HealthUnknown:
		return "3i", true
	}
	return "", false
}

// validValue meldet, ob ein Wert ins Line Protocol passt; NaN und Unendlich gehen nicht.
func validValue(v any) bool {
	switch v := v.(type) {
	case bool, int64, int, string:
		return true
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0)
	}
	return false
}

// appendValue hängt einen gültigen Wert im Format des Line Protocol an.
func appendValue(buf []byte, v any) []byte {
	switch v := v.(type) {
	case bool:
		return strconv.AppendBool(buf, v)
	case int64:
		return append(strconv.AppendInt(buf, v, 10), 'i')
	case int:
		return append(strconv.AppendInt(buf, int64(v), 10), 'i')
	case float64:
		return strconv.AppendFloat(buf, v, 'g', -1, 64)
	case string:
		buf = append(buf, '"')
		buf = append(buf, fieldStringEscaper.Replace(v)...)
		return append(buf, '"')
	}
	return buf
}

func intField[T ~int | ~int64 | ~uint64](name string, v T) string {
	return name + "=" + strconv.FormatInt(int64(v), 10) + "i"
}

type tagSet [][2]string

// tags baut den Teil ",tag=wert…" einer Zeile; leere Tags fallen weg.
func tags(ts tagSet) string {
	var b strings.Builder
	for _, t := range ts {
		if t[1] != "" {
			b.WriteString("," + escapeKey(t[0]) + "=" + escapeKey(t[1]))
		}
	}
	return b.String()
}

var (
	fieldStringEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	keyEscaper         = strings.NewReplacer(",", `\,`, "=", `\=`, " ", `\ `)
	measurementEscaper = strings.NewReplacer(",", `\,`, " ", `\ `)
)

func escapeKey(s string) string         { return keyEscaper.Replace(s) }
func escapeMeasurement(s string) string { return measurementEscaper.Replace(s) }

func nonNil(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
