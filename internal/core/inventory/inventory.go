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

// Kind ist die Rolle eines Elements. Gerät, Gruppe und Zugang sind reserviert und werden nur
// vom Core angelegt; alle anderen Werte kommen aus dem Vokabular der Treiber.
type Kind string

const (
	KindDevice Kind = "device"
	KindGroup  Kind = "group"
	KindAccess Kind = "access" // Wurzel einer Quelle im Gerät, ohne ExternalID
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
	ExternalID      string
	GlobalRef       string
	Transport       string
	Direction       Direction
	Name            string
	Attributes      json.RawMessage
	Position        *int
	Actions         []string
	CriticalActions []string
	Settings        json.RawMessage
	Params          json.RawMessage // Parameter-Schemas von Aktionen mit eigenem Schema (Sende-Ziele)

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

// Identity ist die Identität eines Geräts, wie eine Quelle sie meldet.
type Identity struct {
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
	Identity Identity // vom Treiber

	// AccessElementID ist das Zugangs-Element der Quelle, die Wurzel ihrer Elemente.
	AccessElementID uuid.UUID
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
	Sources  []Source
	Elements []Element
	Links    []Link
}

// Discovery ist das Ergebnis eines Discovery-Laufs einer Quelle.
type Discovery struct {
	SourceID uuid.UUID
	Identity *Identity // nil = unverändert
	// GlobalRef ist die globale ID des Geräts aus Sicht dieser Quelle (am Zugangs-Element);
	// nil = unverändert, "" = keine.
	GlobalRef *string

	// Upsert enthält neue und geänderte Elemente, Seen alle in diesem Lauf gemeldeten.
	Upsert []Element
	Seen   []uuid.UUID

	AddLinks    []Link
	RemoveLinks []uuid.UUID
}
