// Package influx schreibt Fakten, Health, Rollups und eigene Kennzahlen im festen Takt als Line
// Protocol nach InfluxDB (docs/contracts/influx.md).
package influx

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
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

// lines bildet alle Zeilen eines Takts mit dem Zeitstempel t.
func lines(v *core.View, t time.Time, field Vocabulary, me own, gauges Gauges) []string {
	ts := strconv.FormatInt(t.UnixMilli(), 10)
	var out []string
	g := v.Graph
	ids := slices.SortedFunc(func(yield func(uuid.UUID) bool) {
		for id := range g.All() {
			if !yield(id) {
				return
			}
		}
	}, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	for _, id := range ids {
		el, _ := g.Element(id)
		src, _ := g.Source(el.SourceID)
		if el.SourceID != uuid.Nil && v.Sources[el.SourceID].State == core.StateLive {
			tags := tagSet{{"element_id", id.String()}, {"device_id", el.DeviceID.String()}, {"source_id", el.SourceID.String()},
				{"driver", src.Driver}, {"kind", string(el.Kind)}, {"transport", el.Transport}}
			out = append(out, factLines(v.Facts[id], tags, ts, field)...)
		}
		h, ok := v.Health[id]
		if !ok {
			continue
		}
		if level, ok := healthLevel(h.Health); ok {
			tags := tagSet{{"element_id", id.String()}, {"device_id", nonNil(el.DeviceID)}, {"kind", string(el.Kind)}}
			out = append(out, line("health", tags, []string{"level=" + level}, ts))
		}
		if r := h.Rollup; r != nil {
			tags := tagSet{{"element_id", id.String()}, {"kind", string(el.Kind)}}
			out = append(out, line("rollup", tags, []string{
				intField("ok", r.OK), intField("warning", r.Warning), intField("error", r.Error), intField("unknown", r.Unknown),
				intField("unchecked", r.Unchecked), intField("unmonitored", r.Unmonitored), intField("missing", r.Missing),
			}, ts))
		}
	}
	return append(out, monikaLine(v, me, gauges, ts))
}

// factLines bildet pro Measurement einen Punkt mit allen historisierten Feldern; eine Rate nur,
// wenn das Element ihren Zähler nicht meldet. Rohe Fakten nie.
func factLines(facts map[string]any, tags tagSet, ts string, field Vocabulary) []string {
	byMeasurement := map[string][]string{}
	for key, value := range facts {
		f, ok := field(key)
		if !ok || !f.Historized {
			continue
		}
		if _, counter := facts[f.RateOf]; f.RateOf != "" && counter {
			continue
		}
		if v, ok := fieldValue(value); ok {
			byMeasurement[f.Measurement] = append(byMeasurement[f.Measurement], escapeKey(f.Name)+"="+v)
		}
	}
	var out []string
	for _, m := range slices.Sorted(func(yield func(string) bool) {
		for m := range byMeasurement {
			if !yield(m) {
				return
			}
		}
	}) {
		fields := byMeasurement[m]
		slices.Sort(fields)
		out = append(out, line(m, tags, fields, ts))
	}
	return out
}

func monikaLine(v *core.View, me own, gauges Gauges, ts string) string {
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
	if gauges != nil {
		for name, value := range gauges() {
			fields = append(fields, intField(name, value))
		}
	}
	slices.Sort(fields)
	return line(vocab.MeasurementMonika, nil, fields, ts)
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

// fieldValue formatiert einen Wert für das Line Protocol; NaN und Unendlich gehen nicht.
func fieldValue(v any) (string, bool) {
	switch v := v.(type) {
	case bool:
		return strconv.FormatBool(v), true
	case int64:
		return strconv.FormatInt(v, 10) + "i", true
	case int:
		return strconv.Itoa(v) + "i", true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", false
		}
		return strconv.FormatFloat(v, 'g', -1, 64), true
	case string:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`, true
	}
	return "", false
}

func intField[T ~int | ~int64 | ~uint64](name string, v T) string {
	return name + "=" + strconv.FormatInt(int64(v), 10) + "i"
}

type tagSet [][2]string

// line baut eine Zeile; leere Tags fallen weg, Tags in fester Reihenfolge.
func line(measurement string, tags tagSet, fields []string, ts string) string {
	var b strings.Builder
	b.WriteString(escapeMeasurement(measurement))
	for _, t := range tags {
		if t[1] == "" {
			continue
		}
		b.WriteString("," + escapeKey(t[0]) + "=" + escapeKey(t[1]))
	}
	b.WriteString(" " + strings.Join(fields, ",") + " " + ts)
	return b.String()
}

var (
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
