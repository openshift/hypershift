// Package meta provides data types for collectd meta data.
//
// Meta data can be associated with value lists (api.ValueList) and
// notifications (not yet implemented in the collectd Go API).
package meta

import (
	"encoding/json"
	"fmt"
	"math"
)

type entryType int

const (
	_ entryType = iota
	metaStringType
	metaInt64Type
	metaUInt64Type
	metaFloat64Type
	metaBoolType
)

// Data is a map of meta data values. No setter and getter methods are
// implemented for this, callers are expected to add and remove entries as they
// would from a normal map.
type Data map[string]Entry

// Clone returns a copy of d.
func (d Data) Clone() Data {
	if d == nil {
		return nil
	}

	cpy := make(Data)
	for k, v := range d {
		cpy[k] = v
	}
	return cpy
}

// Entry is an entry in the metadata set. The typed value may be bool, float64,
// int64, uint64, or string.
type Entry struct {
	s string
	i int64
	u uint64
	f float64
	b bool

	typ entryType
}

// Bool returns a new bool Entry.
func Bool(b bool) Entry { return Entry{b: b, typ: metaBoolType} }

// Float64 returns a new float64 Entry.
func Float64(f float64) Entry { return Entry{f: f, typ: metaFloat64Type} }

// Int64 returns a new int64 Entry.
func Int64(i int64) Entry { return Entry{i: i, typ: metaInt64Type} }

// UInt64 returns a new uint64 Entry.
func UInt64(u uint64) Entry { return Entry{u: u, typ: metaUInt64Type} }

// String returns a new string Entry.
func String(s string) Entry { return Entry{s: s, typ: metaStringType} }

// Bool returns the bool value of e.
func (e Entry) Bool() (value, ok bool) { return e.b, e.typ == metaBoolType }

// Float64 returns the float64 value of e.
func (e Entry) Float64() (float64, bool) { return e.f, e.typ == metaFloat64Type }

// Int64 returns the int64 value of e.
func (e Entry) Int64() (int64, bool) { return e.i, e.typ == metaInt64Type }

// UInt64 returns the uint64 value of e.
func (e Entry) UInt64() (uint64, bool) { return e.u, e.typ == metaUInt64Type }

// String returns a string representation of e.
func (e Entry) String() string {
	switch e.typ {
	case metaBoolType:
		return fmt.Sprintf("%v", e.b)
	case metaFloat64Type:
		return fmt.Sprintf("%.15g", e.f)
	case metaInt64Type:
		return fmt.Sprintf("%v", e.i)
	case metaUInt64Type:
		return fmt.Sprintf("%v", e.u)
	case metaStringType:
		return e.s
	default:
		return fmt.Sprintf("%v", nil)
	}
}

// IsString returns true if e is a string value.
func (e Entry) IsString() bool {
	return e.typ == metaStringType
}

// Interface returns e's value. It is intended to be used with type switches
// and when printing an entry's type with the "%T" formatting.
func (e Entry) Interface() interface{} {
	switch e.typ {
	case metaBoolType:
		return e.b
	case metaFloat64Type:
		return e.f
	case metaInt64Type:
		return e.i
	case metaUInt64Type:
		return e.u
	case metaStringType:
		return e.s
	default:
		return nil
	}
}

// MarshalJSON implements the "encoding/json".Marshaller interface.
func (e Entry) MarshalJSON() ([]byte, error) {
	switch e.typ {
	case metaBoolType:
		return json.Marshal(e.b)
	case metaFloat64Type:
		if math.IsNaN(e.f) {
			return json.Marshal(nil)
		}
		return json.Marshal(e.f)
	case metaInt64Type:
		return json.Marshal(e.i)
	case metaUInt64Type:
		return json.Marshal(e.u)
	case metaStringType:
		return json.Marshal(e.s)
	default:
		return json.Marshal(nil)
	}
}

// UnmarshalJSON implements the "encoding/json".Unmarshaller interface.
func (e *Entry) UnmarshalJSON(raw []byte) error {
	var b *bool
	if json.Unmarshal(raw, &b) == nil && b != nil {
		*e = Bool(*b)
		return nil
	}

	var s *string
	if json.Unmarshal(raw, &s) == nil && s != nil {
		*e = String(*s)
		return nil
	}

	var i *int64
	if json.Unmarshal(raw, &i) == nil && i != nil {
		*e = Int64(*i)
		return nil
	}

	var u *uint64
	if json.Unmarshal(raw, &u) == nil && u != nil {
		*e = UInt64(*u)
		return nil
	}

	var f *float64
	if json.Unmarshal(raw, &f) == nil {
		if f != nil {
			*e = Float64(*f)
		} else {
			*e = Float64(math.NaN())
		}
		return nil
	}

	return fmt.Errorf("unable to parse %q as meta entry", raw)
}
