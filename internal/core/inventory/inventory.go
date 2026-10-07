// Package inventory hält das Inventar von Monika: Geräte, Quellen, Elemente und Links.
//
// Leere Werte bedeuten „nicht gesetzt“: "" bei Texten, uuid.Nil bei Verweisen,
// time.Time{} bei Zeitpunkten.
package inventory

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Kind ist die Rolle eines Elements. Die Klassen Gerät und Gruppe verwaltet der Core;
// alle anderen Werte kommen aus dem Vokabular der Treiber.
type Kind string

const (
	KindDevice Kind = "device"
	KindGroup  Kind = "group"
)

// Direction ist die Fähigkeit eines Anschlusses; die aktuelle Richtung ist ein Fakt.
type Direction string

const (
	DirectionIn    Direction = "in"
	DirectionOut   Direction = "out"
	DirectionBidir Direction = "bidir"
)

// LinkType ist die Art einer Beziehung zwischen zwei Elementen.
type LinkType string

const (
	LinkFeeds      LinkType = "feeds"      // Signalfluss: From speist To
	LinkUses       LinkType = "uses"       // Ressourcenbindung: From nutzt To
	LinkMember     LinkType = "member"     // Gruppenmitgliedschaft: From ist die Gruppe
	LinkRepresents LinkType = "represents" // Perspektive: From ist Stellvertreter von To
)

// Element ist alles, was Monika kennt: Geräte, Gruppen und Treiber-Elemente.
type Element struct {
	ID       uuid.UUID
	Kind     Kind
	ParentID uuid.UUID
	DeviceID uuid.UUID
	SourceID uuid.UUID

	// vom Treiber
	ExternalID string
	GlobalRef  string
	Transport  string
	Direction  Direction
	Name       string
	Attributes json.RawMessage
	Position   *int
	Actions    []string
	Settings   json.RawMessage

	// vom Core
	Present     bool
	FirstSeenAt time.Time
	LastSeenAt  time.Time

	// vom User
	Label     string
	Notes     string
	Monitored bool
}

// UserFields sind die Spalten eines Elements, die nur der User pflegt.
type UserFields struct {
	Label     string
	Notes     string
	Monitored bool
}

// Device ist die Identität eines Geräts; ID ist die ID seines Elements.
type Device struct {
	ID        uuid.UUID
	Vendor    string
	Model     string
	Serial    string
	Firmware  string
	DeletedAt time.Time
}

// Identity ist die Identität eines Geräts, wie eine Quelle sie meldet.
type Identity struct {
	Name     string
	Vendor   string
	Model    string
	Serial   string
	Firmware string
}

// Source beschreibt, wie ein Gerät erreicht wird.
type Source struct {
	ID       uuid.UUID
	DeviceID uuid.UUID
	Driver   string
	Address  string
	Config   json.RawMessage
	SecretID uuid.UUID
	Enabled  bool
}

// Link ist eine Beziehung zwischen zwei Elementen. Ein noch unbekannter Endpunkt steht
// als global_ref in FromRef bzw. ToRef.
type Link struct {
	ID       uuid.UUID
	Type     LinkType
	SourceID uuid.UUID
	FromID   uuid.UUID
	FromRef  string
	ToID     uuid.UUID
	ToRef    string

	// nur bei LinkMember
	Position *int
	Label    string
}

// Snapshot ist das gesamte Inventar, wie es beim Start geladen wird.
type Snapshot struct {
	Devices  []Device
	Sources  []Source
	Elements []Element
	Links    []Link
}

// Discovery ist das Ergebnis eines Discovery-Laufs einer Quelle.
type Discovery struct {
	SourceID uuid.UUID
	DeviceID uuid.UUID
	Identity *Identity // nil = unverändert

	// Upsert enthält neue und geänderte Elemente, Seen alle in diesem Lauf gemeldeten.
	Upsert []Element
	Seen   []uuid.UUID

	AddLinks    []Link
	RemoveLinks []uuid.UUID
}
