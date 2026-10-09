package postgres

import "encoding/json"

// Die Fachpakete verwenden leere Werte für „nicht gesetzt“, sqlc Pointer für NULL.

// nullable liefert nil für den leeren Wert, sonst einen Pointer auf v.
func nullable[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

// value liefert den Wert hinter p oder den leeren Wert bei nil.
func value[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// convert wandelt einen optionalen Wert in einen anderen Typ, nil bleibt nil.
func convert[From, To any](p *From, f func(From) To) *To {
	if p == nil {
		return nil
	}
	return new(f(*p))
}

// mapSlice wandelt jedes Element von s mit f um.
func mapSlice[From, To any](s []From, f func(From) To) []To {
	out := make([]To, len(s))
	for i, v := range s {
		out[i] = f(v)
	}
	return out
}

// nonNil ersetzt nil durch einen leeren Slice für Array-Spalten mit NOT NULL.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// affected meldet notFound, wenn eine Änderung keine Zeile getroffen hat.
func affected(n int64, err error, notFound error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound
	}
	return nil
}

// jsonObject ersetzt ein fehlendes JSON durch {} für Spalten mit NOT NULL.
func jsonObject(m json.RawMessage) json.RawMessage {
	if len(m) == 0 {
		return json.RawMessage(`{}`)
	}
	return m
}
