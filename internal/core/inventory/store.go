package inventory

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Store persistiert das Inventar. Jede Methode ist eine in sich atomare Operation.
type Store interface {
	// Load lädt das gesamte Inventar als konsistenten Stand.
	Load(ctx context.Context) (Snapshot, error)

	// ApplyDiscovery schreibt das Ergebnis eines Discovery-Laufs und liefert die IDs der
	// Elemente, die in diesem Lauf verschwunden sind.
	ApplyDiscovery(ctx context.Context, d Discovery) (missing []uuid.UUID, err error)

	// CreateDevice legt ein Gerät an; verwendet ID, Label und Notes.
	CreateDevice(ctx context.Context, e Element) error
	// DeleteDevice löscht ein Gerät weich. Solange Presets es verwenden: *DeviceInUseError.
	DeleteDevice(ctx context.Context, id uuid.UUID) error

	CreateSource(ctx context.Context, s Source) error
	// UpdateSource ändert Adresse, Konfiguration, Secret und Aktivierung einer Quelle.
	UpdateSource(ctx context.Context, s Source) error

	// CreateGroup legt eine Gruppe an; verwendet ID, Label und Notes.
	CreateGroup(ctx context.Context, e Element) error
	// CreatePlannedElement legt ein Treiber-Element an, das der Treiber noch nie gesehen hat.
	CreatePlannedElement(ctx context.Context, e Element) error
	UpdateUserFields(ctx context.Context, id uuid.UUID, f UserFields) error

	// AddGroupMember nimmt ein Element in eine Gruppe auf (Link vom Typ member).
	// Würde dadurch ein Zyklus entstehen: ErrMemberCycle.
	AddGroupMember(ctx context.Context, l Link) error
	// UpdateGroupMember ändert Position und Label einer Mitgliedschaft.
	UpdateGroupMember(ctx context.Context, l Link) error
	RemoveGroupMember(ctx context.Context, id uuid.UUID) error
}

var (
	// ErrNotFound bedeutet, dass das Objekt nicht existiert oder nicht geändert werden darf.
	ErrNotFound = errors.New("not found")
	// ErrMemberCycle bedeutet, dass die Mitgliedschaft einen Zyklus zwischen Gruppen schließen würde.
	ErrMemberCycle = errors.New("member link would create a cycle")
)

// PresetRef verweist auf ein Preset.
type PresetRef struct {
	ID   uuid.UUID
	Name string
}

// DeviceInUseError bedeutet, dass Presets noch Schritte mit Zielen am Gerät haben.
type DeviceInUseError struct {
	Presets []PresetRef
}

func (e *DeviceInUseError) Error() string {
	return fmt.Sprintf("device is used by %d preset(s)", len(e.Presets))
}
