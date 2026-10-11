package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/lukirs95/monika/sdk/vocab"
)

// Direction ist die Richtung einer Regel; pro Element, Fakt und Richtung gewinnt die
// spezifischste Stufe.
type Direction string

const (
	DirectionUpper Direction = "upper" // lt, lte
	DirectionLower Direction = "lower" // gt, gte
	DirectionEqual Direction = "equal" // eq, ne, in, not_in
)

// Direction liefert die Richtung des Operators; "" bei einem unbekannten.
func (op Op) Direction() Direction {
	switch op {
	case OpLt, OpLte:
		return DirectionUpper
	case OpGt, OpGte:
		return DirectionLower
	case OpEq, OpNe, OpIn, OpNotIn:
		return DirectionEqual
	}
	return ""
}

// num ist eine Zahl aus einem Fakt oder einer Regel. Ganze Zahlen vergleicht es exakt als int64,
// sonst als float64.
type num struct {
	f     float64
	i     int64
	exact bool // i gilt
}

func (a num) cmp(b num) int {
	if a.exact && b.exact {
		return cmpOrdered(a.i, b.i)
	}
	return cmpOrdered(a.f, b.f)
}

func cmpOrdered[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// factNum wandelt den Wert eines Fakts in eine Zahl.
func factNum(v any) (num, bool) {
	switch v := v.(type) {
	case int64:
		return num{f: float64(v), i: v, exact: true}, true
	case int:
		return num{f: float64(v), i: int64(v), exact: true}, true
	case float64:
		return num{f: v}, true
	}
	return num{}, false
}

// parseValue liest den Wert einer Regel: bool, Zahl (num), String oder eine Liste daraus.
func parseValue(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("value %s: %w", raw, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("value %s: trailing data", raw)
	}
	return convertValue(v, true)
}

func convertValue(v any, listOK bool) (any, error) {
	switch v := v.(type) {
	case bool, string:
		return v, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil || math.IsInf(f, 0) {
			return nil, fmt.Errorf("number %s out of range", v)
		}
		n := num{f: f}
		if i, err := v.Int64(); err == nil {
			n.i, n.exact = i, true
		}
		return n, nil
	case []any:
		if !listOK {
			return nil, errors.New("nested lists are not allowed")
		}
		list := make([]any, len(v))
		for i, item := range v {
			c, err := convertValue(item, false)
			if err != nil {
				return nil, err
			}
			list[i] = c
		}
		return list, nil
	case nil:
		return nil, errors.New("null is not a value")
	}
	return nil, fmt.Errorf("objects are not allowed")
}

// valueFits meldet, ob ein einzelner Wert zum Typ eines Fakts passt; eine Zahl passt zu Int und
// Float.
func valueFits(v any, t vocab.Type) bool {
	switch v.(type) {
	case bool:
		return t == vocab.Bool
	case string:
		return t == vocab.String
	case num:
		return t == vocab.Int || t == vocab.Float
	}
	return false
}

// holds prüft fact <op> value. ok ist false, wenn der Typ des Fakts nicht zum Wert passt.
func holds(op Op, fact, value any) (result, ok bool) {
	switch op {
	case OpEq, OpNe:
		eq, ok := equal(fact, value)
		return eq == (op == OpEq), ok
	case OpIn, OpNotIn:
		list, _ := value.([]any)
		for _, item := range list {
			eq, ok := equal(fact, item)
			if !ok {
				return false, false
			}
			if eq {
				return op == OpIn, true
			}
		}
		return op == OpNotIn, true
	case OpLt, OpLte, OpGt, OpGte:
		f, okF := factNum(fact)
		v, okV := value.(num)
		if !okF || !okV {
			return false, false
		}
		c := f.cmp(v)
		switch op {
		case OpLt:
			return c < 0, true
		case OpLte:
			return c <= 0, true
		case OpGt:
			return c > 0, true
		default:
			return c >= 0, true
		}
	}
	return false, false
}

func equal(fact, value any) (eq, ok bool) {
	switch v := value.(type) {
	case bool:
		f, ok := fact.(bool)
		return f == v, ok
	case string:
		f, ok := fact.(string)
		return f == v, ok
	case num:
		f, ok := factNum(fact)
		return ok && f.cmp(v) == 0, ok
	}
	return false, false
}
