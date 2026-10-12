package core

import (
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/sdk"
)

// SourceState ist der Zustand einer Quelle, wie die View ihn zeigt.
type SourceState string

const (
	StateStarting    SourceState = "starting"
	StateConnecting  SourceState = "connecting"
	StateLive        SourceState = "live"
	StateBackoff     SourceState = "backoff"
	StateUnreachable SourceState = "unreachable"
	StateStopping    SourceState = "stopping"
	StateStuck       SourceState = "stuck"
	StateDisabled    SourceState = "disabled"
)

// SourceStatus ist der Zustand einer Quelle aus Reporter und Supervisor.
type SourceStatus struct {
	State     SourceState
	Since     time.Time     // Beginn des aktuellen Zustands (Zeit der Runde, die ihn festgestellt hat)
	Heartbeat time.Duration // Source.Heartbeat() der laufenden Instanz
	LastSeen  time.Time
	Error     string
}

// DBStatus: Die Datenbank ist erreichbar, wenn der letzte Ping und die letzten Schreibvorgänge
// gelungen sind, höchstens 5 s alt.
type DBStatus struct {
	OK    bool
	Since time.Time // Beginn des aktuellen Zustands
	Error string
}

// Stats sind die Kennzahlen der Schleife (docs/OPERATIONS.md, Measurement monika).
type Stats struct {
	LoopLag            time.Duration // Wartezeit der ältesten Nachricht der letzten Runde
	LoopQueue          int
	Subscribers        int
	SubscribersDropped uint64
	Events             uint64
}

// View ist ein unveränderlicher, in sich stimmiger Stand. Jede Runde der Schleife veröffentlicht
// eine neue (Version steigt); Teile, die sich nicht geändert haben, sind mit der vorigen geteilt.
// Wer sie hält, schreibt nie hinein.
type View struct {
	Version  uint64
	Graph    *inventory.Graph
	Facts    map[uuid.UUID]sdk.Facts
	Health   map[uuid.UUID]evaluation.ElementHealth
	Findings map[uuid.UUID]evaluation.Finding
	Rules    *evaluation.Rules
	Runs     map[uuid.UUID]action.Run
	Sources  map[uuid.UUID]SourceStatus
	DB       DBStatus
	History  InfluxStatus
	Stats    Stats
}

// InScope ist die Auflösung von Geltungsbereichen (inventory.Graph.InScope).
func (v *View) InScope(scope, id uuid.UUID) bool { return v.Graph.InScope(scope, id) }

// EffectiveRules liefert pro Fakt und Richtung die geltenden und die überschriebenen Regeln.
func (v *View) EffectiveRules(id uuid.UUID) []evaluation.RuleSet {
	return v.Rules.EffectiveRules(viewState{v}, id)
}

// viewState liest die View als evaluation.State.
type viewState struct{ v *View }

func (s viewState) Graph() *inventory.Graph      { return s.v.Graph }
func (s viewState) Facts(id uuid.UUID) sdk.Facts { return s.v.Facts[id] }
func (s viewState) Live(id uuid.UUID) bool       { return s.v.Sources[id].State == StateLive }
func (s viewState) Heartbeat(id uuid.UUID) time.Duration {
	return s.v.Sources[id].Heartbeat
}
func (s viewState) Unreachable(id uuid.UUID) (string, bool) {
	st := s.v.Sources[id]
	return st.Error, st.State == StateUnreachable
}
