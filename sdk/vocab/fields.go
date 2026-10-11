package vocab

import (
	"slices"
	"strings"
)

// Field ist ein Fakt des Vokabulars: Measurement und Feld in InfluxDB, Schlüssel
// <measurement>.<feld> in Regeln und UI.
type Field struct {
	Measurement string
	Name        string
	Type        Type
	Kind        Kind
	Historized  bool   // geht nach InfluxDB
	RateOf      string // nur bei Raten: Schlüssel des Zählers
}

// Key ist der Fakt-Schlüssel <measurement>.<feld>.
func (f Field) Key() string { return f.Measurement + "." + f.Name }

// Kern-Fakten im Measurement state; generische UI, Sammelaktionen und Gruppenregeln stützen
// sich nur auf sie.
const (
	StateRunning          = "state.running"
	StateSignalPresent    = "state.signal_present"
	StateConnected        = "state.connected"
	StateReachable        = "state.reachable"
	StateCurrentDirection = "state.current_direction"
)

// Fakten der Sende-Ziele.
const (
	CallOK       = "call.ok"
	CallDuration = "call.duration_ms"
	CallCalledAt = "call.called_at"
)

// fields ist das Vokabular aus docs/contracts/vocabulary.md. Die Einheit steht als Endung im
// Feldnamen; der Typ folgt aus ihr (vocab_test.go prüft das).
var fields = []Field{
	st("state", "running", Bool), st("state", "signal_present", Bool), st("state", "connected", Bool),
	st("state", "reachable", Bool), st("state", "current_direction", String),

	ga("system", "temperature_c", Float), ga("system", "fan_speed_rpm", Int), ga("system", "power_w", Float),
	ga("system", "voltage_v", Float), ga("system", "cpu_percent", Float), ga("system", "memory_percent", Float),
	ga("system", "disk_percent", Float), ga("system", "uptime_s", Int), st("system", "psu_ok", Bool),
	ga("system", "licenses_used", Int), ga("system", "licenses_total", Int),

	st("reference", "locked", Bool), st("reference", "height", Int), st("reference", "frame_rate", Float),
	st("reference", "frame_rate_num", Int), st("reference", "frame_rate_den", Int), st("reference", "scan", String),
	ga("reference", "offset_ns", Int),

	st("timecode", "present", Bool), st("timecode", "drop_frame", Bool),

	st("sdi", "level", String), st("sdi", "payload_id", String), ga("sdi", "amplitude_mv", Float),
	ga("sdi", "cable_length_m", Float), ga("sdi", "timing_jitter_ui", Float), ga("sdi", "alignment_jitter_ui", Float),
	co("sdi", "crc_errors"), co("sdi", "anc_checksum_errors"),

	st("video", "width", Int), st("video", "height", Int), st("video", "frame_rate", Float),
	st("video", "frame_rate_num", Int), st("video", "frame_rate_den", Int), st("video", "scan", String),
	st("video", "sampling", String), st("video", "bit_depth", Int), st("video", "colorimetry", String),
	st("video", "transfer", String), st("video", "range", String), st("video", "afd", Int),
	st("video", "captions_present", Bool), st("video", "black", Bool), st("video", "freeze", Bool),
	co("video", "gamut_errors"),

	st("audio", "sample_rate_hz", Int), st("audio", "bit_depth", Int), st("audio", "channels", Int),
	st("audio", "channel_order", String), st("audio", "encoding", String), st("audio", "packet_time_ns", Int),
	st("audio", "silence", Bool),

	st("aes3", "valid", Bool), co("aes3", "crc_errors"),

	st("ethernet", "link_up", Bool), ga("ethernet", "speed_mbps", Float),
	co("ethernet", "rx_bytes"), co("ethernet", "tx_bytes"), co("ethernet", "rx_packets"), co("ethernet", "tx_packets"),
	co("ethernet", "rx_errors"), co("ethernet", "tx_errors"), co("ethernet", "rx_discards"), co("ethernet", "tx_discards"),

	st("sfp", "rx_los", Bool), st("sfp", "tx_fault", Bool), ga("sfp", "temperature_c", Float),
	ga("sfp", "voltage_v", Float), ga("sfp", "tx_bias_ma", Float), ga("sfp", "tx_power_dbm", Float),
	ga("sfp", "rx_power_dbm", Float),

	st("ptp", "state", String), ga("ptp", "offset_ns", Int), ga("ptp", "path_delay_ns", Int),
	st("ptp", "grandmaster_id", String), st("ptp", "clock_class", Int), st("ptp", "domain", Int),
	ga("ptp", "steps_removed", Int),

	ga("stream", "bitrate_mbps", Float), co("stream", "rx_packets"), co("stream", "tx_packets"),
	co("stream", "packets_lost"), co("stream", "packets_out_of_order"), co("stream", "packets_retransmitted"),
	co("stream", "packets_recovered"), co("stream", "packets_dropped"), ga("stream", "jitter_ns", Int),
	ga("stream", "rtt_ms", Int), ga("stream", "latency_ms", Int), ga("stream", "bandwidth_mbps", Float),
	st("stream", "dest_address", String), st("stream", "dest_port", Int),

	ga("st2110", "offset_ns", Int), st("st2110", "sender_type", String), ga("st2110", "cinst_max", Int),
	ga("st2110", "vrx_max", Int), st("st2110", "compliant", Bool),

	co("st2022_7", "packets_unrecovered"), ga("st2022_7", "path_skew_ms", Int),

	co("tr101290", "sync_loss_errors"), co("tr101290", "cc_errors"), co("tr101290", "pat_errors"),
	co("tr101290", "pmt_errors"), co("tr101290", "pcr_accuracy_errors"),

	st("codec", "format", String), ga("codec", "bitrate_mbps", Float), ga("codec", "latency_ms", Int),
	co("codec", "rx_frames"), co("codec", "frames_dropped"),

	st("tally", "program", Bool), st("tally", "preview", Bool),

	st("gpio", "state", Bool),

	st("call", "ok", Bool), ga("call", "duration_ms", Int),
	{Measurement: "call", Name: "called_at", Type: String, Kind: Status, Historized: false},
}

func st(m, n string, t Type) Field {
	return Field{Measurement: m, Name: n, Type: t, Kind: Status, Historized: true}
}
func ga(m, n string, t Type) Field {
	return Field{Measurement: m, Name: n, Type: t, Kind: Gauge, Historized: true}
}
func co(m, n string) Field {
	return Field{Measurement: m, Name: n, Type: Int, Kind: Counter, Historized: true}
}

var byKey = func() map[string]Field {
	m := make(map[string]Field, len(fields))
	for _, f := range fields {
		m[f.Key()] = f
	}
	return m
}()

// Lookup liefert den Fakt zum Schlüssel, auch die Rate <zähler>_per_s eines Zählers. Rohe
// Fakten (<treiber>.<name>) und der reservierte Fakt present stehen nicht im Vokabular.
func Lookup(key string) (Field, bool) {
	if f, ok := byKey[key]; ok {
		return f, true
	}
	base, ok := strings.CutSuffix(key, RateSuffix)
	if !ok {
		return Field{}, false
	}
	c, ok := byKey[base]
	if !ok || c.Kind != Counter {
		return Field{}, false
	}
	// Historisiert wird die Rate nur, wenn das Element den Zähler nicht meldet; das entscheidet
	// der Influx-Schreiber pro Element.
	return Field{Measurement: c.Measurement, Name: c.Name + RateSuffix, Type: Float, Kind: Gauge, Historized: true, RateOf: base}, true
}

// Fields liefert alle Fakten des Vokabulars ohne die abgeleiteten Raten.
func Fields() []Field { return slices.Clone(fields) }

// Measurement meldet, ob name ein Measurement des Vokabulars ist; die reservierten
// (health, rollup, monika) zählen mit.
func Measurement(name string) bool {
	if name == MeasurementHealth || name == MeasurementRollup || name == MeasurementMonika {
		return true
	}
	return slices.ContainsFunc(fields, func(f Field) bool { return f.Measurement == name })
}
