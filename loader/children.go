package loader

import (
	"strconv"

	"github.com/ChristopherDavenport/jsonschema/ir"
)

// schemaChild is a subschema together with the escaped JSON-pointer suffix
// (relative to its parent) at which it appears.
type schemaChild struct {
	path   string
	schema *ir.Schema
}

// childList returns every direct subschema of s with its pointer suffix. Keys
// of object-valued keywords are JSON-pointer escaped; array members use their
// index.
func childList(s *ir.Schema) []schemaChild {
	var out []schemaChild
	add := func(path string, c *ir.Schema) {
		if c != nil {
			out = append(out, schemaChild{path, c})
		}
	}

	for k, v := range s.Defs {
		add("$defs/"+escapePointer(k), v)
	}
	for k, v := range s.Properties {
		add("properties/"+escapePointer(k), v)
	}
	for k, v := range s.PatternProperties {
		add("patternProperties/"+escapePointer(k), v)
	}
	for k, v := range s.DependentSchemas {
		add("dependentSchemas/"+escapePointer(k), v)
	}
	for i, v := range s.AllOf {
		add("allOf/"+strconv.Itoa(i), v)
	}
	for i, v := range s.AnyOf {
		add("anyOf/"+strconv.Itoa(i), v)
	}
	for i, v := range s.OneOf {
		add("oneOf/"+strconv.Itoa(i), v)
	}
	for i, v := range s.PrefixItems {
		add("prefixItems/"+strconv.Itoa(i), v)
	}
	add("not", s.Not)
	add("if", s.If)
	add("then", s.Then)
	add("else", s.Else)
	add("items", s.Items)
	add("contains", s.Contains)
	add("additionalProperties", s.AdditionalProperties)
	add("propertyNames", s.PropertyNames)
	add("unevaluatedProperties", s.UnevaluatedProperties)
	add("unevaluatedItems", s.UnevaluatedItems)
	add("contentSchema", s.ContentSchema)
	return out
}
