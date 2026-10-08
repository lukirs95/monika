package inventory

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Store persistiert das Inventar. Jede Methode ist eine in sich atomare Operation.
//
// Methoden, die der User auslöst, bekommen den handelnden Benutzer als actor und schreiben
// einen Eintrag ins Audit; uuid.Nil steht für Monika selbst.
type Store interface {
	// Load lädt das gesamte Inventar als konsistenten Stand.
	Load(ctx context.Context) (Snapshot, error)

	// ApplyDiscovery schreibt das Ergebnis eines Discovery-Laufs und liefert die IDs der
	// Elemente, die in diesem Lauf verschwunden sind.
	ApplyDiscovery(ctx context.Context, d Discovery) (missing []uuid.UUID, err error)

	// CreateDevice legt ein Gerät an; verwendet ID, Label und Notes.
	CreateDevice(ctx context.Context, actor uuid.UUID, e Element) error
	// DeleteDevice löscht ein Gerät weich. Solange Presets es verwenden: *InUseError.
	DeleteDevice(ctx context.Context, actor uuid.UUID, id uuid.UUID) error

	// CreateSource legt eine Quelle samt Zugangs-Element (ID accessID) an. Ist das Gerät
	// unbekannt oder gelöscht: ErrNotFound.
	CreateSource(ctx context.Context, actor uuid.UUID, s Source, accessID uuid.UUID) error
	// UpdateSource ändert Adresse, Konfiguration, Secret und Aktivierung einer Quelle.
	UpdateSource(ctx context.Context, actor uuid.UUID, s Source) error

	// CreateGroup legt eine Gruppe an; verwendet ID, Label und Notes.
	CreateGroup(ctx context.Context, actor uuid.UUID, e Element) error
	// DeleteGroup löscht eine Gruppe weich. Solange Presets sie verwenden: *InUseError.
	DeleteGroup(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
	// CreatePlannedElement legt ein Treiber-Element an, das der Treiber noch nie gesehen hat.
	CreatePlannedElement(ctx context.Context, actor uuid.UUID, e Element) error
	UpdateUserFields(ctx context.Context, actor uuid.UUID, id uuid.UUID, f UserFields) error

	// AddGroupMember nimmt ein Element in eine Gruppe auf (Link vom Typ member).
	// Würde dadurch ein Zyklus entstehen: ErrMemberCycle.
	AddGroupMember(ctx context.Context, actor uuid.UUID, l Link) error
	// UpdateGroupMember ändert Position und Label einer Mitgliedschaft.
	UpdateGroupMember(ctx context.Context, actor uuid.UUID, l Link) error
	RemoveGroupMember(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
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

// InUseError bedeutet, dass Presets noch Schritte mit Zielen am Gerät bzw. an der Gruppe haben.
type InUseError struct {
	Presets []PresetRef
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("used by %d preset(s)", len(e.Presets))
}
