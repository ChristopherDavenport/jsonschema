package ir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
)

// rat unmarshals a JSON number into an exact rational.
type rat struct{ v *big.Rat }

func (r *rat) UnmarshalJSON(b []byte) error {
	v := new(big.Rat)
	if _, ok := v.SetString(string(bytes.TrimSpace(b))); !ok {
		return fmt.Errorf("ir: invalid number %q", b)
	}
	r.v = v
	return nil
}

// count unmarshals a JSON number used as a non-negative size limit. The spec
// permits it to be written as a decimal (e.g. 2.0), so we truncate to uint64.
type count struct{ v uint64 }

func (c *count) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("ir: invalid size %q: %w", b, err)
	}
	c.v = uint64(f)
	return nil
}

func countval(c *count) *uint64 {
	if c == nil {
		return nil
	}
	return &c.v
}

// rawSchema mirrors the on-the-wire shape across draft-07, 2019-09 and 2020-12.
// Legacy spellings (items-as-array, additionalItems, dependencies,
// $recursive*) are captured here and normalized in UnmarshalJSON.
type rawSchema struct {
	ID              *string            `json:"$id"`
	SchemaURI       *string            `json:"$schema"`
	Vocabulary      map[string]bool    `json:"$vocabulary"`
	Anchor          *string            `json:"$anchor"`
	DynamicAnchor   *string            `json:"$dynamicAnchor"`
	Ref             *string            `json:"$ref"`
	DynamicRef      *string            `json:"$dynamicRef"`
	RecursiveAnchor *bool              `json:"$recursiveAnchor"`
	RecursiveRef    *string            `json:"$recursiveRef"`
	Defs            map[string]*Schema `json:"$defs"`
	Definitions     map[string]*Schema `json:"definitions"`
	Comment         *string            `json:"$comment"`

	Title       *string `json:"title"`
	Description *string `json:"description"`
	Deprecated  *bool   `json:"deprecated"`
	ReadOnly    *bool   `json:"readOnly"`
	WriteOnly   *bool   `json:"writeOnly"`
	Examples    []any   `json:"examples"`

	AllOf            []*Schema          `json:"allOf"`
	AnyOf            []*Schema          `json:"anyOf"`
	OneOf            []*Schema          `json:"oneOf"`
	Not              *Schema            `json:"not"`
	If               *Schema            `json:"if"`
	Then             *Schema            `json:"then"`
	Else             *Schema            `json:"else"`
	DependentSchemas map[string]*Schema `json:"dependentSchemas"`

	Properties            map[string]*Schema `json:"properties"`
	PatternProperties     map[string]*Schema `json:"patternProperties"`
	AdditionalProperties  *Schema            `json:"additionalProperties"`
	PropertyNames         *Schema            `json:"propertyNames"`
	UnevaluatedProperties *Schema            `json:"unevaluatedProperties"`

	PrefixItems      []*Schema       `json:"prefixItems"`
	Items            json.RawMessage `json:"items"`
	AdditionalItems  *Schema         `json:"additionalItems"`
	Contains         *Schema         `json:"contains"`
	UnevaluatedItems *Schema         `json:"unevaluatedItems"`

	Type *TypeSet `json:"type"`

	MultipleOf       *rat `json:"multipleOf"`
	Maximum          *rat `json:"maximum"`
	ExclusiveMaximum *rat `json:"exclusiveMaximum"`
	Minimum          *rat `json:"minimum"`
	ExclusiveMinimum *rat `json:"exclusiveMinimum"`

	MaxLength *count  `json:"maxLength"`
	MinLength *count  `json:"minLength"`
	Pattern   *string `json:"pattern"`

	MaxItems    *count `json:"maxItems"`
	MinItems    *count `json:"minItems"`
	UniqueItems *bool  `json:"uniqueItems"`
	MaxContains *count `json:"maxContains"`
	MinContains *count `json:"minContains"`

	MaxProperties     *count              `json:"maxProperties"`
	MinProperties     *count              `json:"minProperties"`
	Required          []string            `json:"required"`
	DependentRequired map[string][]string `json:"dependentRequired"`

	Format *string `json:"format"`

	ContentEncoding  *string `json:"contentEncoding"`
	ContentMediaType *string `json:"contentMediaType"`
	ContentSchema    *Schema `json:"contentSchema"`

	// dependencies is the draft-07 keyword split in 2019-09+ into
	// dependentRequired (array value) and dependentSchemas (schema value).
	Dependencies map[string]json.RawMessage `json:"dependencies"`

	XGo *XGo `json:"x-go"`
}

// knownKeywords is the set of object keys the ir model recognizes (all standard
// JSON Schema keywords across the supported drafts, plus the `x-go` extension).
// Any other key is captured into Schema.Extra.
var knownKeywords = map[string]bool{
	"$id": true, "$schema": true, "$vocabulary": true, "$anchor": true,
	"$dynamicAnchor": true, "$ref": true, "$dynamicRef": true,
	"$recursiveAnchor": true, "$recursiveRef": true, "$defs": true,
	"definitions": true, "$comment": true, "title": true, "description": true,
	"deprecated": true, "readOnly": true, "writeOnly": true, "examples": true,
	"allOf": true, "anyOf": true, "oneOf": true, "not": true, "if": true,
	"then": true, "else": true, "dependentSchemas": true, "properties": true,
	"patternProperties": true, "additionalProperties": true,
	"propertyNames": true, "unevaluatedProperties": true, "prefixItems": true,
	"items": true, "additionalItems": true, "contains": true,
	"unevaluatedItems": true, "type": true, "multipleOf": true, "maximum": true,
	"exclusiveMaximum": true, "minimum": true, "exclusiveMinimum": true,
	"maxLength": true, "minLength": true, "pattern": true, "maxItems": true,
	"minItems": true, "uniqueItems": true, "maxContains": true,
	"minContains": true, "maxProperties": true, "minProperties": true,
	"required": true, "dependentRequired": true, "format": true,
	"contentEncoding": true, "contentMediaType": true, "contentSchema": true,
	"dependencies": true, "const": true, "default": true, "enum": true,
	"x-go": true,
}

// UnmarshalJSON decodes a schema, which may be a boolean or an object.
func (s *Schema) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)

	// Boolean schema form.
	if len(trimmed) > 0 && (trimmed[0] == 't' || trimmed[0] == 'f') {
		var b bool
		if err := json.Unmarshal(trimmed, &b); err == nil {
			*s = Schema{Boolean: &b}
			return nil
		}
	}

	// Detect key presence so AlwaysValid can treat `{}` as trivially valid.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("ir: schema must be a boolean or object: %w", err)
	}

	var raw rawSchema
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*s = Schema{hasKeywords: len(keys) > 0}
	if err := s.fromRaw(&raw); err != nil {
		return err
	}

	// Capture source key order of `properties`, which the map above loses.
	if msg, ok := keys["properties"]; ok {
		order, err := objectKeyOrder(msg)
		if err != nil {
			return fmt.Errorf("ir: properties: %w", err)
		}
		s.PropertyOrder = order
	}

	// Capture unrecognized keys (custom vendor annotations) into Extra.
	for k, v := range keys {
		if !knownKeywords[k] {
			if s.Extra == nil {
				s.Extra = map[string]json.RawMessage{}
			}
			s.Extra[k] = v
		}
	}

	// const/default/enum are handled from the raw key set so that a present
	// JSON null is distinguishable from an absent keyword, and so numbers are
	// decoded exactly (UseNumber) for value-equality.
	if msg, ok := keys["const"]; ok {
		v, err := decodeAny(msg)
		if err != nil {
			return fmt.Errorf("ir: const: %w", err)
		}
		s.Const = &v
	}
	if msg, ok := keys["default"]; ok {
		v, err := decodeAny(msg)
		if err != nil {
			return fmt.Errorf("ir: default: %w", err)
		}
		s.Default = &v
	}
	if msg, ok := keys["enum"]; ok {
		var items []json.RawMessage
		if err := json.Unmarshal(msg, &items); err != nil {
			return fmt.Errorf("ir: enum: %w", err)
		}
		s.Enum = make([]any, len(items))
		for i, it := range items {
			v, err := decodeAny(it)
			if err != nil {
				return fmt.Errorf("ir: enum[%d]: %w", i, err)
			}
			s.Enum[i] = v
		}
	}
	return nil
}

func (s *Schema) fromRaw(raw *rawSchema) error {
	strptr(&s.ID, raw.ID)
	strptr(&s.SchemaURI, raw.SchemaURI)
	s.Vocabulary = raw.Vocabulary
	strptr(&s.Anchor, raw.Anchor)
	strptr(&s.DynamicAnchor, raw.DynamicAnchor)
	strptr(&s.Ref, raw.Ref)
	strptr(&s.DynamicRef, raw.DynamicRef)
	strptr(&s.Comment, raw.Comment)

	// $recursiveRef / $recursiveAnchor (2019-09) map onto the dynamic machinery:
	// $recursiveRef becomes a fragment-less $dynamicRef, and $recursiveAnchor is
	// tracked as its own flag (a nameless recursive anchor).
	if raw.RecursiveRef != nil && s.DynamicRef == "" {
		s.DynamicRef = *raw.RecursiveRef
	}
	if raw.RecursiveAnchor != nil && *raw.RecursiveAnchor {
		s.RecursiveAnchor = true
	}

	// $defs, with draft-07 `definitions` merged in ($defs wins on conflict).
	if len(raw.Definitions) > 0 || len(raw.Defs) > 0 {
		s.Defs = make(map[string]*Schema, len(raw.Definitions)+len(raw.Defs))
		for k, v := range raw.Definitions {
			s.Defs[k] = v
		}
		for k, v := range raw.Defs {
			s.Defs[k] = v
		}
	}

	strptr(&s.Title, raw.Title)
	strptr(&s.Description, raw.Description)
	boolptr(&s.Deprecated, raw.Deprecated)
	boolptr(&s.ReadOnly, raw.ReadOnly)
	boolptr(&s.WriteOnly, raw.WriteOnly)
	s.Examples = raw.Examples

	s.AllOf = raw.AllOf
	s.AnyOf = raw.AnyOf
	s.OneOf = raw.OneOf
	s.Not = raw.Not
	s.If = raw.If
	s.Then = raw.Then
	s.Else = raw.Else
	s.DependentSchemas = raw.DependentSchemas

	s.Properties = raw.Properties
	s.PatternProperties = raw.PatternProperties
	s.AdditionalProperties = raw.AdditionalProperties
	s.PropertyNames = raw.PropertyNames
	s.UnevaluatedProperties = raw.UnevaluatedProperties

	s.PrefixItems = raw.PrefixItems
	s.Contains = raw.Contains
	s.UnevaluatedItems = raw.UnevaluatedItems
	if err := s.normalizeItems(raw); err != nil {
		return err
	}

	if raw.Type != nil {
		s.Type = *raw.Type
	}

	s.MultipleOf = ratval(raw.MultipleOf)
	s.Maximum = ratval(raw.Maximum)
	s.ExclusiveMaximum = ratval(raw.ExclusiveMaximum)
	s.Minimum = ratval(raw.Minimum)
	s.ExclusiveMinimum = ratval(raw.ExclusiveMinimum)

	s.MaxLength = countval(raw.MaxLength)
	s.MinLength = countval(raw.MinLength)
	strptr(&s.Pattern, raw.Pattern)

	s.MaxItems = countval(raw.MaxItems)
	s.MinItems = countval(raw.MinItems)
	if raw.UniqueItems != nil {
		s.UniqueItems = *raw.UniqueItems
	}
	s.MaxContains = countval(raw.MaxContains)
	s.MinContains = countval(raw.MinContains)

	s.MaxProperties = countval(raw.MaxProperties)
	s.MinProperties = countval(raw.MinProperties)
	s.Required = raw.Required
	s.DependentRequired = raw.DependentRequired

	strptr(&s.Format, raw.Format)
	strptr(&s.ContentEncoding, raw.ContentEncoding)
	strptr(&s.ContentMediaType, raw.ContentMediaType)
	s.ContentSchema = raw.ContentSchema
	s.XGo = raw.XGo

	return s.normalizeDependencies(raw)
}

// normalizeItems reconciles the draft-07 tuple form (`items` as an array plus
// `additionalItems`) with the 2020-12 form (`prefixItems` plus `items`).
func (s *Schema) normalizeItems(raw *rawSchema) error {
	items := bytes.TrimSpace(raw.Items)
	switch {
	case len(items) == 0:
		// No `items`. A draft-07 `additionalItems` alone still becomes `items`.
		if raw.AdditionalItems != nil && len(s.PrefixItems) > 0 {
			s.Items = raw.AdditionalItems
		}
	case items[0] == '[':
		// draft-07 tuple: items is an array of schemas -> prefixItems.
		var tuple []*Schema
		if err := json.Unmarshal(items, &tuple); err != nil {
			return fmt.Errorf("ir: items tuple: %w", err)
		}
		s.PrefixItems = append(s.PrefixItems, tuple...)
		if raw.AdditionalItems != nil {
			s.Items = raw.AdditionalItems
		}
	default:
		// 2020-12 form: items is a single schema.
		var sub Schema
		if err := json.Unmarshal(items, &sub); err != nil {
			return fmt.Errorf("ir: items: %w", err)
		}
		s.Items = &sub
	}
	return nil
}

// normalizeDependencies splits the draft-07 `dependencies` keyword into
// dependentRequired (array values) and dependentSchemas (schema values).
func (s *Schema) normalizeDependencies(raw *rawSchema) error {
	for name, msg := range raw.Dependencies {
		trimmed := bytes.TrimSpace(msg)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			var names []string
			if err := json.Unmarshal(trimmed, &names); err != nil {
				return fmt.Errorf("ir: dependencies[%q]: %w", name, err)
			}
			if s.DependentRequired == nil {
				s.DependentRequired = map[string][]string{}
			}
			s.DependentRequired[name] = names
			continue
		}
		var sub Schema
		if err := json.Unmarshal(trimmed, &sub); err != nil {
			return fmt.Errorf("ir: dependencies[%q]: %w", name, err)
		}
		if s.DependentSchemas == nil {
			s.DependentSchemas = map[string]*Schema{}
		}
		s.DependentSchemas[name] = &sub
	}
	return nil
}

// objectKeyOrder returns the keys of a JSON object in source order. It streams
// tokens so the order is preserved (decoding into a map would lose it).
func objectKeyOrder(data []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected object, got %v", tok)
	}
	var order []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		order = append(order, key.(string))
		// Skip the value (which may be a nested object/array) in full.
		if err := skipValue(dec); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// skipValue consumes one complete JSON value from dec, descending into nested
// objects and arrays so the next token is the following key.
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); ok && (d == '{' || d == '[') {
		for dec.More() {
			if d == '{' {
				if _, err := dec.Token(); err != nil { // key
					return err
				}
			}
			if err := skipValue(dec); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // closing delim
			return err
		}
	}
	return nil
}

// decodeAny decodes JSON into an any, using UseNumber so numbers are preserved
// exactly as json.Number for value-equality comparisons.
func decodeAny(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func strptr(dst *string, src *string) {
	if src != nil {
		*dst = *src
	}
}

func boolptr(dst *bool, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func ratval(r *rat) *big.Rat {
	if r == nil {
		return nil
	}
	return r.v
}
