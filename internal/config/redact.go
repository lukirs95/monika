package config

import "log/slog"

const redacted = "[redacted]"

// Secret ist ein geheimer Wert aus der Konfiguration (Datenbank-URL mit Passwort, Influx-Token,
// Admin-Passwort). Beim Ausgeben und Loggen erscheint er nur als [redacted]; den Klartext gibt
// Reveal an genau der Stelle heraus, die ihn braucht.
type Secret struct{ value string }

// Reveal liefert den Klartext.
func (s Secret) Reveal() string { return s.value }

// IsZero meldet, ob der Wert leer ist.
func (s Secret) IsZero() bool { return s.value == "" }

func (s Secret) String() string               { return redacted }
func (s Secret) GoString() string             { return redacted }
func (s Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
