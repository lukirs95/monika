package sdk

import (
	"encoding/json"
	"time"

	"github.com/lukirs95/monika/sdk/vocab"
)

// FactType ist der Typ eines Fakts: Bool, Int, Float oder String.
type FactType = vocab.Type

// FactKind ist die Art eines Fakts: Status, Gauge oder Counter.
type FactKind = vocab.Kind

// FactDef deklariert einen rohen Fakt im Katalog eines Treibers.
type FactDef struct {
	Key         string // <treiber>.<name>, Einheit als Endung
	Type        FactType
	Kind        FactKind
	Description string
}

// Command ist ein Befehl an ein Element.
type Command struct {
	ExternalID string
	Action     string
	Params     json.RawMessage
}

// Status ist die Antwort des Geräts auf einen Befehl.
type Status string

const (
	Accepted Status = "accepted"
	Rejected Status = "rejected"
)

// Result ist das Ergebnis von Execute. Ob der Befehl gewirkt hat, zeigt der nächste Update.
type Result struct {
	Status         Status
	Detail         string
	RejectedFields []string
	Response       json.RawMessage
}

// ActionDef beschreibt eine Aktion im Katalog des Treibers.
type ActionDef struct {
	Name     string          // vocab.Action* oder <treiber>.<name>
	Params   json.RawMessage // JSON-Schema der Parameter
	Verify   []Condition     // Prüfbedingung, Platzhalter {{param}} möglich
	Timeout  time.Duration
	Critical bool
}

// Condition ist eine Bedingung fact <op> value; Value ist ein JSON-Wert als Text.
type Condition struct {
	Fact  string
	Op    string
	Value string
}

// Typen von Einstellungen.
const (
	SettingInt    = "int"
	SettingFloat  = "float"
	SettingBool   = "bool"
	SettingString = "string"
	SettingEnum   = "enum"
	SettingIPv4   = "ipv4"
)

// Setting ist ein schreibbarer Fakt-Schlüssel mit Grenzen. Die JSON-Form steht in
// element.settings und in der API.
type Setting struct {
	Type      string   `json:"type"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Values    []string `json:"values,omitempty"` // bei enum
	Critical  bool     `json:"critical,omitempty"`
	WriteOnly bool     `json:"write_only,omitempty"` // das Gerät meldet den Wert nicht zurück: kein Fakt, keine Prüfung
}

// Param ist ein Parameter einer Aktion mit eigenem Schema (Sende-Ziele), mit Standardwert.
type Param struct {
	Setting
	Default any `json:"default,omitempty"` // bool, int64, float64 oder string
}

// FactTypeOf liefert den Typ des Fakts, den eine Einstellung dieses Typs setzt.
func FactTypeOf(settingType string) (FactType, bool) {
	switch settingType {
	case SettingInt:
		return vocab.Int, true
	case SettingFloat:
		return vocab.Float, true
	case SettingBool:
		return vocab.Bool, true
	case SettingString, SettingEnum, SettingIPv4:
		return vocab.String, true
	}
	return "", false
}

// ValueOK meldet, ob v genau vom Go-Typ zu t ist.
func ValueOK(t FactType, v any) bool {
	switch v.(type) {
	case bool:
		return t == vocab.Bool
	case int64:
		return t == vocab.Int
	case float64:
		return t == vocab.Float
	case string:
		return t == vocab.String
	}
	return false
}
