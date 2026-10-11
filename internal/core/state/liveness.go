package state

import (
	"time"

	"github.com/google/uuid"
)

// Phase ist der Zustand einer laufenden Quelle (sdk.md, Fehler, Panic und Neustart).
type Phase string

const (
	PhaseStarting    Phase = "starting"    // Start beauftragt, Run läuft noch nicht
	PhaseConnecting  Phase = "connecting"  // Run läuft, noch nicht verbunden oder Snapshot fehlt
	PhaseLive        Phase = "live"        // verbunden, Snapshot übernommen, Lebenszeichen aktuell
	PhaseUnreachable Phase = "unreachable" // nicht Live seit 3 × Heartbeat nach dem letzten Lebenszeichen
	PhaseBackoff     Phase = "backoff"     // New oder Run gescheitert, wartet auf Neustart
	PhaseStopping    Phase = "stopping"    // Run wurde abgebrochen
	PhaseStuck       Phase = "stuck"       // Run kehrt nach dem Abbruch nicht zurück
)

// Status ist die Lebendigkeit einer Quelle zu einem Zeitpunkt.
type Status struct {
	Phase     Phase
	Heartbeat time.Duration
	LastSeen  time.Time // letztes Lebenszeichen; Null-Zeit = noch keines
	Err       string    // Grund bei backoff, stuck bzw. dem letzten Abbruch
}

type source struct {
	phase     Phase
	heartbeat time.Duration
	connected bool      // Connected gemeldet, noch nicht Disconnected
	live      bool      // nach Connected ein Snapshot übernommen
	since     time.Time // Start bzw. letztes Lebenszeichen: ab hier zählen 3 × Heartbeat
	lastSeen  time.Time
	err       string
}

// Liveness verfolgt die Lebendigkeit der laufenden Quellen. Deaktivierte Quellen kennt es nicht.
type Liveness struct {
	sources map[uuid.UUID]*source
}

// NewLiveness liefert eine leere Verfolgung.
func NewLiveness() *Liveness { return &Liveness{sources: map[uuid.UUID]*source{}} }

// Starting: Der Supervisor ist beauftragt, die Quelle zu starten.
func (l *Liveness) Starting(id uuid.UUID, at time.Time) {
	l.sources[id] = &source{phase: PhaseStarting, since: at}
}

// Started: Run läuft (SourceStarted); heartbeat ist Source.Heartbeat().
func (l *Liveness) Started(id uuid.UUID, heartbeat time.Duration, at time.Time) {
	s := l.get(id, at)
	s.phase, s.heartbeat, s.connected, s.live, s.err = PhaseConnecting, heartbeat, false, false, ""
}

// Connected: Die Quelle ist verbunden; Live wird sie erst mit dem Snapshot.
func (l *Liveness) Connected(id uuid.UUID, at time.Time) {
	s := l.get(id, at)
	s.connected, s.live, s.phase = true, false, PhaseConnecting
	s.touch(at)
}

// SnapshotApplied: Der vollständige Stand nach Connected ist übernommen; die Quelle ist Live.
func (l *Liveness) SnapshotApplied(id uuid.UUID, at time.Time) {
	s := l.get(id, at)
	if s.connected {
		s.live, s.phase = true, PhaseLive
	}
	s.touch(at)
}

// Disconnected: Die Verbindung ist weg; der Treiber verbindet selbst neu.
func (l *Liveness) Disconnected(id uuid.UUID, err error, at time.Time) {
	s := l.get(id, at)
	s.connected, s.live, s.phase, s.err = false, false, PhaseConnecting, errText(err)
	s.touch(at)
}

// Touch: ein Lebenszeichen (jede Meldung der Quelle).
func (l *Liveness) Touch(id uuid.UUID, at time.Time) { l.get(id, at).touch(at) }

// Failed: New oder Run ist gescheitert (auch Panic); der Supervisor startet nach einer Wartezeit neu.
func (l *Liveness) Failed(id uuid.UUID, err error, at time.Time) {
	s := l.get(id, at)
	s.connected, s.live, s.phase, s.err = false, false, PhaseBackoff, errText(err)
}

// Stopping: Run wurde abgebrochen.
func (l *Liveness) Stopping(id uuid.UUID, at time.Time) {
	s := l.get(id, at)
	s.connected, s.live, s.phase = false, false, PhaseStopping
}

// Stuck: Run kehrt nach dem Abbruch nicht zurück.
func (l *Liveness) Stuck(id uuid.UUID, at time.Time) {
	s := l.get(id, at)
	s.phase, s.err = PhaseStuck, "driver does not respond"
}

// Remove vergisst die Quelle (gestoppt, deaktiviert oder gelöscht).
func (l *Liveness) Remove(id uuid.UUID) { delete(l.sources, id) }

// Live meldet, ob die Fakten der Quelle bewertet werden dürfen.
func (l *Liveness) Live(id uuid.UUID, now time.Time) bool {
	s, ok := l.sources[id]
	return ok && s.isLive(now)
}

// HasSnapshot meldet, ob nach dem letzten Connected ein Snapshot übernommen wurde. Davor
// verwirft die Schleife Updates.
func (l *Liveness) HasSnapshot(id uuid.UUID) bool {
	s, ok := l.sources[id]
	return ok && s.live
}

// Status liefert die Lebendigkeit der Quelle zu now.
func (l *Liveness) Status(id uuid.UUID, now time.Time) (Status, bool) {
	s, ok := l.sources[id]
	if !ok {
		return Status{}, false
	}
	phase := s.phase
	if s.unreachable(now) && (phase == PhaseConnecting || phase == PhaseLive || phase == PhaseStarting) {
		phase = PhaseUnreachable
	}
	return Status{Phase: phase, Heartbeat: s.heartbeat, LastSeen: s.lastSeen, Err: s.err}, true
}

// IsUnreachable meldet, ob für die Quelle zu now der Befund source.unreachable gilt.
func (l *Liveness) IsUnreachable(id uuid.UUID, now time.Time) bool {
	s, ok := l.sources[id]
	return ok && s.unreachable(now)
}

// Unreachable liefert die Quellen, für die zu now der Befund source.unreachable gilt.
func (l *Liveness) Unreachable(now time.Time) []uuid.UUID {
	var out []uuid.UUID
	for id, s := range l.sources {
		if s.unreachable(now) {
			out = append(out, id)
		}
	}
	return out
}

func (l *Liveness) get(id uuid.UUID, at time.Time) *source {
	s, ok := l.sources[id]
	if !ok {
		s = &source{phase: PhaseStarting, since: at}
		l.sources[id] = s
	}
	return s
}

func (s *source) touch(at time.Time) {
	s.lastSeen, s.since = at, at
}

// isLive: Snapshot nach Connected übernommen und Lebenszeichen nicht älter als 3 × Heartbeat.
func (s *source) isLive(now time.Time) bool {
	return s.live && (s.heartbeat == 0 || now.Sub(s.since) < 3*s.heartbeat)
}

// unreachable: nicht Live seit 3 × Heartbeat nach dem letzten Lebenszeichen bzw. dem Start; bei
// Heartbeat 0 nur bei Backoff und Stuck, dann sofort.
func (s *source) unreachable(now time.Time) bool {
	if s.isLive(now) || s.phase == PhaseStopping {
		return false
	}
	if s.heartbeat == 0 {
		return s.phase == PhaseBackoff || s.phase == PhaseStuck
	}
	return now.Sub(s.since) >= 3*s.heartbeat
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
