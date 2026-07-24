// Package loader parses JSON Schema documents into the ir model and resolves
// the referencing layer: $id/base-URI resolution, $ref/$anchor/$defs, remote
// documents, and cycle-safe indexing of every schema resource by canonical URI.
package loader

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ChristopherDavenport/jsonschema/ir"
)

// Loader holds a registry of schema resources indexed by canonical absolute
// URI. Multiple documents may be added (e.g. remote references) before
// resolving.
type Loader struct {
	resources map[string]*ir.Schema // canonical URI (incl. fragment) -> schema
	// dynByBase indexes $dynamicAnchor per resource base URI: base -> name -> schema.
	dynByBase map[string]map[string]*ir.Schema
	// recursiveByBase indexes the $recursiveAnchor:true schema per base URI.
	recursiveByBase map[string]*ir.Schema
}

// New returns an empty Loader.
func New() *Loader {
	return &Loader{
		resources:       map[string]*ir.Schema{},
		dynByBase:       map[string]map[string]*ir.Schema{},
		recursiveByBase: map[string]*ir.Schema{},
	}
}

// DynamicAnchorInBase returns the schema declaring $dynamicAnchor name within
// the resource identified by base, if any.
func (l *Loader) DynamicAnchorInBase(base, name string) (*ir.Schema, bool) {
	if m, ok := l.dynByBase[base]; ok {
		s, ok := m[name]
		return s, ok
	}
	return nil, false
}

// RecursiveAnchorInBase returns the $recursiveAnchor schema within the resource
// identified by base, if any.
func (l *Loader) RecursiveAnchorInBase(base string) (*ir.Schema, bool) {
	s, ok := l.recursiveByBase[base]
	return s, ok
}

// AddJSON parses a JSON document as a schema, registers it (and every
// subschema) under baseURI, and returns the root schema.
func (l *Loader) AddJSON(baseURI string, data []byte) (*ir.Schema, error) {
	var s ir.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("loader: parse %q: %w", baseURI, err)
	}
	l.AddSchema(baseURI, &s)
	return &s, nil
}

// AddSchema registers an already-parsed schema and its subschemas under
// baseURI, assigning canonical locations.
func (l *Loader) AddSchema(baseURI string, s *ir.Schema) {
	l.walk(s, baseURI, "")
	// Ensure the retrieval URI resolves to the root even if a top-level $id
	// relocated the root's canonical URI.
	if _, ok := l.resources[baseURI+"#"]; !ok {
		l.resources[baseURI+"#"] = s
	}
}

// Resources returns the registry of canonical URI -> schema.
func (l *Loader) Resources() map[string]*ir.Schema { return l.resources }

// walk assigns BaseURI and Location to s and every subschema, and registers
// them. base is the base URI in effect; ptr is the JSON pointer from the
// nearest resource root.
func (l *Loader) walk(s *ir.Schema, base, ptr string) {
	if s == nil {
		return
	}

	// A nested $id starts a new resource: it becomes the base and resets ptr.
	if !s.IsBoolean() && s.ID != "" {
		base = resolveURI(base, stripFragment(s.ID))
		ptr = ""
	}

	s.BaseURI = base
	s.Location = base + "#" + ptr
	l.resources[s.Location] = s

	if s.IsBoolean() {
		return
	}

	if s.Anchor != "" {
		l.resources[base+"#"+s.Anchor] = s
	}
	if s.DynamicAnchor != "" {
		// A $dynamicAnchor is also a plain-name anchor for ordinary $ref.
		l.resources[base+"#"+s.DynamicAnchor] = s
		if l.dynByBase[base] == nil {
			l.dynByBase[base] = map[string]*ir.Schema{}
		}
		l.dynByBase[base][s.DynamicAnchor] = s
	}
	if s.RecursiveAnchor {
		l.recursiveByBase[base] = s
	}

	for _, c := range childList(s) {
		l.walk(c.schema, base, ptr+"/"+c.path)
	}
}

// Resolve resolves a $ref (or $dynamicRef) string against base and returns the
// target schema.
func (l *Loader) Resolve(base, ref string) (*ir.Schema, error) {
	abs := resolveURI(base, ref)
	doc, frag := splitFragment(abs)

	frag, err := url.PathUnescape(frag)
	if err != nil {
		frag = rawFragment(abs)
	}

	// Direct hit on the canonical key.
	if s, ok := l.resources[doc+"#"+frag]; ok {
		return s, nil
	}
	// Empty base: refs of the form "#/..." registered under "#/...".
	if s, ok := l.resources["#"+frag]; ok && doc == "" {
		return s, nil
	}
	// Fall back to walking the JSON pointer from the document root.
	if frag == "" || strings.HasPrefix(frag, "/") {
		if root, ok := l.resources[doc+"#"]; ok {
			if s, err := resolvePointer(root, frag); err == nil {
				return s, nil
			}
		}
	}
	return nil, fmt.Errorf("loader: cannot resolve $ref %q (base %q)", ref, base)
}

// resolvePointer walks an RFC 6901 JSON pointer from root, consuming keyword
// tokens (and their index/key operand where applicable).
func resolvePointer(root *ir.Schema, ptr string) (*ir.Schema, error) {
	if ptr == "" {
		return root, nil
	}
	toks := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	cur := root
	for i := 0; i < len(toks); i++ {
		key := func() string { i++; return unescapePointer(toks[i]) }
		var next *ir.Schema
		switch toks[i] {
		case "$defs", "definitions":
			next = cur.Defs[key()]
		case "properties":
			next = cur.Properties[key()]
		case "patternProperties":
			next = cur.PatternProperties[key()]
		case "dependentSchemas", "dependencies":
			next = cur.DependentSchemas[key()]
		case "allOf":
			next = at(cur.AllOf, key())
		case "anyOf":
			next = at(cur.AnyOf, key())
		case "oneOf":
			next = at(cur.OneOf, key())
		case "prefixItems":
			next = at(cur.PrefixItems, key())
		case "items":
			if i+1 < len(toks) && len(cur.PrefixItems) > 0 {
				next = at(cur.PrefixItems, key())
			} else {
				next = cur.Items
			}
		case "additionalItems":
			next = cur.Items
		case "not":
			next = cur.Not
		case "if":
			next = cur.If
		case "then":
			next = cur.Then
		case "else":
			next = cur.Else
		case "contains":
			next = cur.Contains
		case "additionalProperties":
			next = cur.AdditionalProperties
		case "propertyNames":
			next = cur.PropertyNames
		case "unevaluatedProperties":
			next = cur.UnevaluatedProperties
		case "unevaluatedItems":
			next = cur.UnevaluatedItems
		case "contentSchema":
			next = cur.ContentSchema
		default:
			return nil, fmt.Errorf("loader: unknown pointer segment %q", toks[i])
		}
		if next == nil {
			return nil, fmt.Errorf("loader: pointer %q not found", ptr)
		}
		cur = next
	}
	return cur, nil
}

func at[T any](s []T, idxTok string) T {
	var zero T
	i, err := strconv.Atoi(idxTok)
	if err != nil || i < 0 || i >= len(s) {
		return zero
	}
	return s[i]
}

func resolveURI(base, ref string) string {
	// A fragment-only reference keeps the base document and swaps the fragment.
	// Handling it directly avoids net/url quirks with opaque URIs such as urn:.
	if strings.HasPrefix(ref, "#") {
		return stripFragment(base) + ref
	}
	if base == "" {
		return ref
	}
	bu, err := url.Parse(base)
	if err != nil {
		return ref
	}
	ru, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return bu.ResolveReference(ru).String()
}

func splitFragment(s string) (doc, frag string) {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func stripFragment(s string) string { d, _ := splitFragment(s); return d }
func rawFragment(s string) string   { _, f := splitFragment(s); return f }

func escapePointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func unescapePointer(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}
