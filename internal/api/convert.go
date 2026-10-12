package api

import "github.com/google/uuid"

// optional übersetzt einen leeren Wert in ein fehlendes Feld (null in der Spezifikation).
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// optionalID übersetzt uuid.Nil in ein fehlendes Feld.
func optionalID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
