// Package executor führt Aktionen aus: Ziel auflösen, Läufe anlegen, pro Element nacheinander
// über die Quelle ausführen und gegen den beobachteten Zustand prüfen
// (docs/contracts/services.md, flows.md).
package executor

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/sdk"
)

// Fehler von Submit; die API macht daraus 4xx-Antworten.
var (
	ErrStopped        = errors.New("executor stopped")
	ErrUnknownTarget  = errors.New("unknown target")
	ErrUnsupported    = errors.New("action not supported by the target")
	ErrDriverRequired = errors.New("device level needs a driver")
	ErrInvalidParams  = errors.New("invalid params")
)

// ActionPreset ist die Aktion des Wurzel-Laufs eines Presets; diesen Lauf beendet der
// Preset-Runner, nicht der Executor.
const ActionPreset = "preset"

// Request ist ein Auftrag an den Executor.
type Request struct {
	Actor     uuid.UUID
	ElementID uuid.UUID // Element, Gerät oder Gruppe; beim Wurzel-Lauf eines Presets uuid.Nil
	Action    string
	Params    json.RawMessage
	// DeviceLevel: bei Gerät oder Gruppe als Ziel an die Zugangs-Elemente statt an alle übrigen
	// Elemente; dann ist Driver Pflicht.
	DeviceLevel bool
	Driver      string // Filter auf Quellen dieses Treibers
	// Targets sind vom Preset-Runner eingefrorene Ziele; sie ersetzen die Auflösung.
	Targets      []uuid.UUID
	ParentID     uuid.UUID
	PresetID     uuid.UUID
	PresetStepID uuid.UUID
}

// Core ist, was der Executor vom Core braucht.
type Core interface {
	View() *core.View
	Subscribe(buffer int) (<-chan core.Event, func())
	Post(m core.Message)
}

// Sources führt Befehle an laufenden Quellen aus (der Supervisor).
type Sources interface {
	Execute(ctx context.Context, sourceID uuid.UUID, c sdk.Command) (sdk.Result, error)
}
