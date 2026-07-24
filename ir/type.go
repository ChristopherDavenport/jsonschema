package ir

import (
	"encoding/json"
	"fmt"
)

// Type is one of the seven JSON Schema primitive type names.
type Type uint8

const (
	TypeNull Type = iota
	TypeBoolean
	TypeObject
	TypeArray
	TypeNumber
	TypeString
	TypeInteger
)

var typeNames = map[string]Type{
	"null":    TypeNull,
	"boolean": TypeBoolean,
	"object":  TypeObject,
	"array":   TypeArray,
	"number":  TypeNumber,
	"string":  TypeString,
	"integer": TypeInteger,
}

// String returns the canonical JSON Schema spelling of the type.
func (t Type) String() string {
	for name, v := range typeNames {
		if v == t {
			return name
		}
	}
	return fmt.Sprintf("Type(%d)", uint8(t))
}

// TypeSet is the value of the `type` keyword: zero or more permitted types. An
// empty set means the keyword was absent (no type constraint).
type TypeSet []Type

// Contains reports whether t is a member of the set.
func (ts TypeSet) Contains(t Type) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

// Empty reports whether the `type` keyword was absent.
func (ts TypeSet) Empty() bool { return len(ts) == 0 }

// UnmarshalJSON accepts either a single type name (`"string"`) or an array of
// type names (`["string", "null"]`).
func (ts *TypeSet) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		t, ok := typeNames[single]
		if !ok {
			return fmt.Errorf("ir: unknown type %q", single)
		}
		*ts = TypeSet{t}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("ir: type must be a string or array of strings: %w", err)
	}
	out := make(TypeSet, 0, len(many))
	for _, name := range many {
		t, ok := typeNames[name]
		if !ok {
			return fmt.Errorf("ir: unknown type %q", name)
		}
		out = append(out, t)
	}
	*ts = out
	return nil
}
