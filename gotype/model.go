// Package gotype maps a compiled ir schema graph onto a model of named Go
// types. It decides which schemas become top-level declarations, resolves $ref
// to the type they name, and computes the Go type of every field — the input
// the gen package turns into source.
package gotype

import "github.com/ChristopherDavenport/jsonschema/ir"

// Kind classifies a generated top-level declaration.
type Kind int

const (
	// Struct is an object schema with known properties.
	Struct Kind = iota
	// Enum is a schema constrained to a fixed set of scalar values.
	Enum
	// Alias is a named scalar, slice, or map type.
	Alias
	// Interface is a oneOf/anyOf modeled as a marker interface with variants.
	Interface
)

// Model is the complete set of declarations for one output package.
type Model struct {
	Package string
	Decls   []*Decl
}

// Decl is one named Go type declaration.
type Decl struct {
	Name   string
	Kind   Kind
	Doc    string
	Schema *ir.Schema

	Fields     []*Field   // Kind == Struct
	Enum       []EnumVal  // Kind == Enum
	Underlying *TypeRef   // Kind == Alias (or Enum's base type)
	Variants   []*TypeRef // Kind == Interface (each is a Named variant type)
	Exclusive  bool       // Kind == Interface: true for oneOf, false for anyOf
	Tuple      bool       // Kind == Struct: positional array (prefixItems)
}

// Field is one struct field.
type Field struct {
	Name      string // exported Go identifier
	JSONName  string // the JSON property name
	Doc       string
	Type      *TypeRef
	Required  bool
	ExtraTags []string // additional "key:value" struct tags from x-go
	Schema    *ir.Schema
}

// EnumVal is one enumerated value with its generated const identifier.
type EnumVal struct {
	Ident string
	Value any
}

// TypeRef is a Go type expression: exactly one shape is set.
type TypeRef struct {
	// Named references a generated declaration by Go type name.
	Named string
	// Prim is a builtin or qualified primitive ("string", "int64", "time.Time").
	Prim string
	// Import is the package path required by a qualified Prim (e.g. "time").
	Import string
	// Slice / Map describe composite types; Map is always keyed by string.
	Slice *TypeRef
	Map   *TypeRef
	// Pointer wraps the referent in a pointer.
	Pointer bool
}

// prim builds a primitive TypeRef, optionally with an import path.
func prim(name string, importPath ...string) *TypeRef {
	t := &TypeRef{Prim: name}
	if len(importPath) > 0 {
		t.Import = importPath[0]
	}
	return t
}
