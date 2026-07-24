// Package ir defines the internal representation of a JSON Schema: a single
// canonical model, aligned with draft 2020-12, onto which every supported
// dialect is normalized.
//
// A schema is a sum type. In JSON it is either a boolean (`true`/`false`) or an
// object. That is modeled by [Schema.Boolean]: when non-nil the schema is a
// boolean schema, and every other field is unused. `true` is equivalent to the
// empty object schema `{}` (always valid) and `false` to `{"not": {}}` (always
// invalid).
package ir

import "math/big"

// Schema is a single JSON Schema node.
//
// Fields whose absence is semantically distinct from their zero value are
// pointers or slices/maps, so that "keyword not present" (nil) is
// distinguishable from "keyword present with the zero value".
type Schema struct {
	// Boolean, when non-nil, marks this as a boolean schema. All other fields
	// are ignored. *Boolean == true means always valid; false means never valid.
	Boolean *bool

	// Core keywords and identifiers.
	ID              string             // $id
	SchemaURI       string             // $schema
	Vocabulary      map[string]bool    // $vocabulary
	Anchor          string             // $anchor
	DynamicAnchor   string             // $dynamicAnchor
	Ref             string             // $ref
	DynamicRef      string             // $dynamicRef (or $recursiveRef, normalized)
	RecursiveAnchor bool               // $recursiveAnchor: true (2019-09)
	Defs            map[string]*Schema // $defs
	Comment         string             // $comment

	// Metadata annotations.
	Title       string
	Description string
	Default     *any // presence-tracked; the pointed-to value may be nil (JSON null)
	Deprecated  bool
	ReadOnly    bool
	WriteOnly   bool
	Examples    []any

	// In-place applicators.
	AllOf            []*Schema
	AnyOf            []*Schema
	OneOf            []*Schema
	Not              *Schema
	If               *Schema
	Then             *Schema
	Else             *Schema
	DependentSchemas map[string]*Schema

	// Object child applicators.
	Properties            map[string]*Schema
	PatternProperties     map[string]*Schema
	AdditionalProperties  *Schema
	PropertyNames         *Schema
	UnevaluatedProperties *Schema

	// Array child applicators.
	PrefixItems      []*Schema
	Items            *Schema
	Contains         *Schema
	UnevaluatedItems *Schema

	// Any-type assertions.
	Type  TypeSet
	Enum  []any // decoded JSON values; nil means the keyword is absent
	Const *any  // presence-tracked; the pointed-to value may be nil (JSON null)

	// Numeric assertions. Stored as exact rationals to avoid float rounding.
	MultipleOf       *big.Rat
	Maximum          *big.Rat
	ExclusiveMaximum *big.Rat
	Minimum          *big.Rat
	ExclusiveMinimum *big.Rat

	// String assertions.
	MaxLength *uint64
	MinLength *uint64
	Pattern   string

	// Array assertions.
	MaxItems    *uint64
	MinItems    *uint64
	UniqueItems bool
	MaxContains *uint64
	MinContains *uint64

	// Object assertions.
	MaxProperties     *uint64
	MinProperties     *uint64
	Required          []string
	DependentRequired map[string][]string

	// Semantic format (annotation by default; assertion when enabled).
	Format string

	// Content keywords (annotation-only in the standard vocabularies).
	ContentEncoding  string
	ContentMediaType string
	ContentSchema    *Schema

	// IgnoreSiblings marks a schema whose $ref suppresses all other keywords.
	// This is draft-07 (and earlier) semantics, set by dialect normalization;
	// in 2019-09+ a $ref applies alongside its siblings.
	IgnoreSiblings bool

	// XGo carries the `x-go` vendor extension used to steer code generation.
	XGo *XGo

	// Location is set by the loader: the canonical absolute URI (base + JSON
	// pointer/anchor) at which this schema resource resides. It is the identity
	// used for $ref resolution and for one-type-per-resource code generation.
	Location string

	// BaseURI is the base URI in effect for resolving relative references that
	// appear within this schema. Set by the loader.
	BaseURI string

	// hasKeywords records whether the source object carried at least one
	// recognized keyword. It lets AlwaysValid treat `{}` as trivially valid.
	hasKeywords bool
}

// XGo is the `x-go` vendor extension: per-schema overrides for code generation.
type XGo struct {
	Type      string   // fully-qualified Go type to use verbatim, e.g. "time.Time"
	Import    string   // import path required by Type, e.g. "time"
	Name      string   // override the generated type/field identifier
	Pointer   *bool    // force (or forbid) a pointer wrapper
	ExtraTags []string // additional struct tags, e.g. `validate:"required"`
}

// IsBoolean reports whether s is a boolean schema (`true`/`false`).
func (s *Schema) IsBoolean() bool { return s != nil && s.Boolean != nil }

// AlwaysValid reports whether s unconditionally accepts every instance. This is
// true for `true`, for the empty object schema `{}`, and for a nil schema.
func (s *Schema) AlwaysValid() bool {
	if s == nil {
		return true
	}
	if s.Boolean != nil {
		return *s.Boolean
	}
	return !s.hasKeywords
}

// AlwaysInvalid reports whether s unconditionally rejects every instance
// (the boolean schema `false`).
func (s *Schema) AlwaysInvalid() bool {
	return s != nil && s.Boolean != nil && !*s.Boolean
}
