// Package core ist das Herz von Monika: Eine Schleife besitzt Inventar, Fakten, Lebenszeichen und
// Bewertung, nimmt Befehle, Nachrichten und Meldungen der Treiber an, veröffentlicht nach jeder
// Runde eine unveränderliche View und verteilt Ereignisse (docs/contracts/core.md).
package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/action"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
)

// Command ist ein Befehl der API an die Schleife (Core.Do). Die Rechte prüft die API vorher;
// neue IDs vergibt sie (UUIDv7).
type Command interface{ command() }

type (
	CreateDevice struct {
		Actor  uuid.UUID
		Device inventory.Element
	}
	DeleteDevice struct{ Actor, ID uuid.UUID }
	CreateSource struct {
		Actor  uuid.UUID
		Source inventory.Source
	}
	UpdateSource struct {
		Actor  uuid.UUID
		Source inventory.Source
	}
	DeleteSource struct{ Actor, ID uuid.UUID }
	CreateGroup  struct {
		Actor uuid.UUID
		Group inventory.Element
	}
	DeleteGroup      struct{ Actor, ID uuid.UUID }
	UpdateUserFields struct {
		Actor, ID uuid.UUID
		Fields    inventory.UserFields
	}
	AddGroupMember struct {
		Actor uuid.UUID
		Link  inventory.Link
	}
	UpdateGroupMember struct {
		Actor uuid.UUID
		Link  inventory.Link
	}
	RemoveGroupMember struct{ Actor, ID uuid.UUID }
	CreateRule        struct {
		Actor uuid.UUID
		Rule  evaluation.Rule
	}
	UpdateRule struct {
		Actor uuid.UUID
		Rule  evaluation.Rule
	}
	DeleteRule struct{ Actor, ID uuid.UUID }
	AckFinding struct{ Actor, ID uuid.UUID }
	// ReplaceSecret ersetzt den Wert eines Secrets und startet alle Quellen neu, die es verwenden.
	ReplaceSecret struct {
		Actor, ID uuid.UUID
		Value     json.RawMessage
	}
)

func (CreateDevice) command()      {}
func (DeleteDevice) command()      {}
func (CreateSource) command()      {}
func (UpdateSource) command()      {}
func (DeleteSource) command()      {}
func (CreateGroup) command()       {}
func (DeleteGroup) command()       {}
func (UpdateUserFields) command()  {}
func (AddGroupMember) command()    {}
func (UpdateGroupMember) command() {}
func (RemoveGroupMember) command() {}
func (CreateRule) command()        {}
func (UpdateRule) command()        {}
func (DeleteRule) command()        {}
func (AckFinding) command()        {}
func (ReplaceSecret) command()     {}

// Message ist eine Nachricht eines Dienstes an die Schleife (Core.Post), ohne Antwort.
type Message interface{ message() }

type (
	// SourceStarted: Der Supervisor hat die Quelle gestartet; Heartbeat ist Source.Heartbeat().
	SourceStarted struct {
		SourceID  uuid.UUID
		Heartbeat time.Duration
	}
	// SourceStopped: Run ist nach einem Stop zurückgekehrt.
	SourceStopped struct{ SourceID uuid.UUID }
	// SourceFailed: Run ist mit Fehler oder Panic zurückgekehrt; der Supervisor wartet (Backoff).
	SourceFailed struct {
		SourceID uuid.UUID
		Err      error
		Panic    bool
	}
	// SourceStuck: Run ist nach dem Stopp nicht in der Frist zurückgekehrt.
	SourceStuck struct{ SourceID uuid.UUID }
	// InfluxStatus ist der Zustand des Influx-Schreibers (docs/contracts/influx.md).
	InfluxStatus struct {
		OK        bool
		Buffered  int
		Dropped   int
		LastError string
	}
	// DBPing ist das Ergebnis eines Pings des Datenbank-Wächters.
	DBPing struct {
		OK  bool
		Err error
	}
)

func (SourceStarted) message() {}
func (SourceStopped) message() {}
func (SourceFailed) message()  {}
func (SourceStuck) message()   {}
func (InfluxStatus) message()  {}
func (DBPing) message()        {}
func (RunChanged) message()    {}

// SourceSpec beschreibt eine Quelle für den Supervisor.
type SourceSpec struct {
	SourceID uuid.UUID
	DeviceID uuid.UUID
	Driver   string
	Address  string
	Config   json.RawMessage
	SecretID uuid.UUID
}

// Supervisor startet und stoppt Quellen im Auftrag der Schleife; beide kehren sofort zurück.
type Supervisor interface {
	Start(spec SourceSpec)
	Stop(sourceID uuid.UUID)
}

// Secrets ersetzt den Wert eines Secrets (secret.Service).
type Secrets interface {
	Replace(ctx context.Context, actor, id uuid.UUID, value json.RawMessage) error
}

// RunChanged meldet einen geänderten Lauf: als Nachricht vom Executor, als Ereignis von der
// Schleife (dann mit Version).
type RunChanged struct {
	Run     action.Run
	version uint64
}

func (e RunChanged) Version() uint64 { return e.version }
