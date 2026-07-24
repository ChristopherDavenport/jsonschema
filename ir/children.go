package ir

// ChildSchemas returns every direct subschema of s, in no particular order.
// It is used by tree-walking passes (reference indexing, dialect normalization)
// that need to visit the whole schema graph.
func ChildSchemas(s *Schema) []*Schema {
	if s == nil || s.IsBoolean() {
		return nil
	}
	var out []*Schema
	add := func(c *Schema) {
		if c != nil {
			out = append(out, c)
		}
	}
	for _, v := range s.Defs {
		add(v)
	}
	for _, v := range s.Properties {
		add(v)
	}
	for _, v := range s.PatternProperties {
		add(v)
	}
	for _, v := range s.DependentSchemas {
		add(v)
	}
	for _, v := range s.AllOf {
		add(v)
	}
	for _, v := range s.AnyOf {
		add(v)
	}
	for _, v := range s.OneOf {
		add(v)
	}
	for _, v := range s.PrefixItems {
		add(v)
	}
	add(s.Not)
	add(s.If)
	add(s.Then)
	add(s.Else)
	add(s.Items)
	add(s.Contains)
	add(s.AdditionalProperties)
	add(s.PropertyNames)
	add(s.UnevaluatedProperties)
	add(s.UnevaluatedItems)
	add(s.ContentSchema)
	return out
}
