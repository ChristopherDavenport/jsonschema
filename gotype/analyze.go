package gotype

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/ChristopherDavenport/jsonschema/ir"
)

// Resolver resolves a $ref against a base URI to the schema it names.
type Resolver interface {
	Resolve(base, ref string) (*ir.Schema, error)
}

// Config controls analysis.
type Config struct {
	Package  string
	RootName string
}

// Analyze walks the compiled schema and produces the Go type model.
func Analyze(root *ir.Schema, res Resolver, cfg Config) (*Model, error) {
	if cfg.Package == "" {
		cfg.Package = "schema"
	}
	if cfg.RootName == "" {
		cfg.RootName = "Root"
	}
	a := &analyzer{
		cfg:       cfg,
		res:       res,
		model:     &Model{Package: cfg.Package},
		nameByLoc: map[string]string{},
		declByLoc: map[string]*Decl{},
		used:      map[string]bool{},
	}

	// Root and every $defs entry become named, top-level declarations.
	a.declare(cfg.RootName, root)
	for _, key := range sortedKeys(root.Defs) {
		a.declare(GoName(key), root.Defs[key])
	}

	// Build declarations, discovering nested named types as we go.
	for i := 0; i < len(a.queue); i++ {
		d := a.queue[i]
		if err := a.build(d); err != nil {
			return nil, err
		}
	}
	return a.model, nil
}

type analyzer struct {
	cfg       Config
	res       Resolver
	model     *Model
	nameByLoc map[string]string
	declByLoc map[string]*Decl
	used      map[string]bool
	queue     []*Decl
}

// declare registers schema as a named declaration, returning the unique name.
func (a *analyzer) declare(want string, schema *ir.Schema) string {
	if name, ok := a.nameByLoc[schema.Location]; ok {
		return name
	}
	if schema.XGo != nil && schema.XGo.Name != "" {
		want = schema.XGo.Name
	}
	name := a.unique(want)
	d := &Decl{Name: name, Schema: schema, Doc: schema.Description}
	a.nameByLoc[schema.Location] = name
	a.declByLoc[schema.Location] = d
	a.model.Decls = append(a.model.Decls, d)
	a.queue = append(a.queue, d)
	return name
}

func (a *analyzer) unique(want string) string {
	if want == "" {
		want = "T"
	}
	name := want
	for i := 2; a.used[name]; i++ {
		name = fmt.Sprintf("%s%d", want, i)
	}
	a.used[name] = true
	return name
}

// build fills in a declaration's kind and body.
func (a *analyzer) build(d *Decl) error {
	s := d.Schema
	switch {
	case xgoType(s) != nil:
		d.Kind = Alias
		d.Underlying = xgoType(s)
	case len(s.Enum) > 0:
		a.buildEnum(d)
	case s.Const != nil:
		u := valueType(*s.Const)
		if u == nil {
			d.Kind = Alias
			d.Underlying = prim("any")
			return nil
		}
		d.Kind = Enum
		d.Underlying = u
		d.Enum = []EnumVal{{Ident: d.Name + "Value", Value: *s.Const}}
	case len(s.OneOf) > 0:
		a.buildInterface(d, s.OneOf, true)
	case len(s.AnyOf) > 0:
		a.buildInterface(d, s.AnyOf, false)
	case a.allOfEmbeddable(s):
		d.Kind = Struct
		return a.buildStruct(d)
	case isObject(s) && len(s.Properties) == 0:
		// An object with no declared properties is a dictionary type.
		d.Kind = Alias
		d.Underlying = a.mapType(s)
	case isObject(s):
		d.Kind = Struct
		return a.buildStruct(d)
	case len(s.PrefixItems) > 0:
		a.buildTuple(d)
	case isArray(s):
		d.Kind = Alias
		elem := prim("any")
		if s.Items != nil {
			elem = a.typeRef(s.Items, d.Name+"Item")
		}
		d.Underlying = &TypeRef{Slice: elem}
	default:
		d.Kind = Alias
		d.Underlying = scalarType(s)
	}
	return nil
}

func (a *analyzer) buildEnum(d *Decl) {
	u := inferEnumType(d.Schema.Enum)
	if u == nil {
		// Heterogeneous enum: cannot be typed Go consts, so alias to any.
		d.Kind = Alias
		d.Underlying = prim("any")
		return
	}
	d.Kind = Enum
	d.Underlying = u
	for _, v := range d.Schema.Enum {
		d.Enum = append(d.Enum, EnumVal{Ident: d.Name + GoName(fmt.Sprint(v)), Value: v})
	}
}

// valueType maps a decoded JSON scalar to its Go type, or nil if it is not a
// scalar (object/array/null) usable as a typed constant.
func valueType(v any) *TypeRef {
	switch n := v.(type) {
	case string:
		return prim("string")
	case bool:
		return prim("bool")
	case interface{ Int64() (int64, error) }: // json.Number
		if _, err := n.Int64(); err == nil {
			return prim("int64")
		}
		return prim("float64")
	default:
		return nil
	}
}

// inferEnumType returns the common scalar type of all enum values, or nil if
// they are not all the same usable scalar type.
func inferEnumType(values []any) *TypeRef {
	var common *TypeRef
	for _, v := range values {
		t := valueType(v)
		if t == nil {
			return nil
		}
		if common == nil {
			common = t
			continue
		}
		if common.Prim != t.Prim {
			// Mix int64/float64 widens to float64; otherwise incompatible.
			if isNumeric(common.Prim) && isNumeric(t.Prim) {
				common = prim("float64")
				continue
			}
			return nil
		}
	}
	return common
}

func isNumeric(p string) bool { return p == "int64" || p == "float64" }

// buildTuple models a prefixItems array as a struct of positional fields with
// tuple JSON marshaling.
func (a *analyzer) buildTuple(d *Decl) {
	d.Kind = Struct
	d.Tuple = true
	for i, ps := range d.Schema.PrefixItems {
		d.Fields = append(d.Fields, &Field{
			Name:     fmt.Sprintf("Elem%d", i),
			JSONName: strconv.Itoa(i),
			Type:     a.typeRef(ps, fmt.Sprintf("%sElem%d", d.Name, i)),
			Required: true,
			Schema:   ps,
		})
	}
}

// buildInterface models a oneOf/anyOf as a marker interface whose variants are
// each a named Go type (so they can carry the marker method).
func (a *analyzer) buildInterface(d *Decl, branches []*ir.Schema, exclusive bool) {
	d.Kind = Interface
	d.Exclusive = exclusive
	for i, b := range branches {
		hint := fmt.Sprintf("%sVariant%d", d.Name, i+1)
		ref := a.typeRef(b, hint)
		d.Variants = append(d.Variants, a.namedVariant(ref, hint))
	}
}

// namedVariant guarantees the variant is a named type. A ref/named type is used
// as-is; anything else (scalar, slice, map) is wrapped in a fresh alias so a
// marker method can be attached.
func (a *analyzer) namedVariant(ref *TypeRef, hint string) *TypeRef {
	if ref.Named != "" {
		return ref
	}
	name := a.unique(hint)
	a.model.Decls = append(a.model.Decls, &Decl{
		Name:       name,
		Kind:       Alias,
		Underlying: ref,
	})
	return &TypeRef{Named: name}
}

func (a *analyzer) buildStruct(d *Decl) error {
	for _, key := range sortedKeys(d.Schema.Properties) {
		ps := d.Schema.Properties[key]
		f := &Field{
			Name:     GoName(key),
			JSONName: key,
			Doc:      ps.Description,
			Type:     a.typeRef(ps, d.Name+GoName(key)),
			Required: contains(d.Schema.Required, key),
			Schema:   ps,
		}
		// Optional scalars and named types become pointers so absence is
		// distinguishable; slices and maps already carry a nil zero value.
		if !f.Required && f.Type.Slice == nil && f.Type.Map == nil {
			f.Type.Pointer = true
		}
		// An x-go extension may override the pointer decision and add tags.
		if ps.XGo != nil {
			f.ExtraTags = ps.XGo.ExtraTags
			if ps.XGo.Pointer != nil {
				f.Type.Pointer = *ps.XGo.Pointer
			}
		}
		d.Fields = append(d.Fields, f)
	}

	// allOf of object schemas is modeled by embedding each member type.
	if a.allOfEmbeddable(d.Schema) {
		for i, m := range d.Schema.AllOf {
			d.Embeds = append(d.Embeds, a.typeRef(m, fmt.Sprintf("%sPart%d", d.Name, i+1)))
		}
		d.AllOfHandled = true
	}
	return nil
}

// allOfEmbeddable reports whether every allOf member will be generated as a Go
// struct (directly or through a $ref), so the composition can be expressed as
// Go struct embedding.
func (a *analyzer) allOfEmbeddable(s *ir.Schema) bool {
	if len(s.AllOf) == 0 || len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return false
	}
	for _, m := range s.AllOf {
		target := m
		if m.Ref != "" {
			r, err := a.res.Resolve(m.BaseURI, m.Ref)
			if err != nil {
				return false
			}
			target = r
		}
		if !embeddableMember(target) {
			return false
		}
	}
	return true
}

// embeddableMember reports whether an allOf member becomes a struct type. Only
// a struct promotes its fields when embedded: an object schema with no declared
// properties becomes a map alias, and an enum/const/union/x-go member becomes
// some other named type — embedding either would give the member a JSON name of
// its own (`{"Dict": {…}}`) instead of merging its properties into the parent.
func embeddableMember(s *ir.Schema) bool {
	if s.IsBoolean() || xgoType(s) != nil {
		return false
	}
	if len(s.Enum) > 0 || s.Const != nil || len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return false
	}
	return isObject(s) && len(s.Properties) > 0
}

// typeRef computes the Go type of a subschema, creating nested named types
// (using hint as the desired name) when the schema is itself structured.
func (a *analyzer) typeRef(s *ir.Schema, hint string) *TypeRef {
	if s == nil || s.AlwaysValid() {
		return prim("any")
	}

	if t := xgoType(s); t != nil {
		return t
	}

	if s.Ref != "" {
		if target, err := a.res.Resolve(s.BaseURI, s.Ref); err == nil {
			name := a.declare(a.refName(s.Ref, hint), target)
			return &TypeRef{Named: name}
		}
		return prim("any")
	}

	switch {
	case len(s.Enum) > 0 || s.Const != nil:
		return &TypeRef{Named: a.declare(hint, s)}
	case len(s.OneOf) > 0 || len(s.AnyOf) > 0:
		return &TypeRef{Named: a.declare(hint, s)}
	case a.allOfEmbeddable(s):
		return &TypeRef{Named: a.declare(hint, s)}
	case isObject(s) && len(s.Properties) == 0:
		return a.mapType(s)
	case isObject(s):
		return &TypeRef{Named: a.declare(hint, s)}
	case len(s.PrefixItems) > 0:
		return &TypeRef{Named: a.declare(hint, s)}
	case isArray(s):
		elem := prim("any")
		if s.Items != nil {
			elem = a.typeRef(s.Items, hint+"Item")
		}
		return &TypeRef{Slice: elem}
	default:
		return scalarType(s)
	}
}

// refName derives a type name from the final segment of a JSON pointer ref.
func (a *analyzer) refName(ref, hint string) string {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '/' {
			return GoName(ref[i+1:])
		}
	}
	return hint
}

// mapType builds a map[string]T for a dictionary object, where T is the
// additionalProperties schema (or any when unconstrained).
func (a *analyzer) mapType(s *ir.Schema) *TypeRef {
	elem := prim("any")
	if s.AdditionalProperties != nil && !s.AdditionalProperties.AlwaysValid() {
		elem = a.typeRef(s.AdditionalProperties, GoName(s.Location)+"Value")
	}
	return &TypeRef{Map: elem}
}

// xgoType returns the Go type declared by an `x-go` extension, or nil.
func xgoType(s *ir.Schema) *TypeRef {
	if s.XGo == nil || s.XGo.Type == "" {
		return nil
	}
	if s.XGo.Import == "" {
		return prim(s.XGo.Type)
	}
	// Qualify: keep only the selector after the last dot for jen.Qual.
	sel := s.XGo.Type
	if i := lastDot(sel); i >= 0 {
		sel = sel[i+1:]
	}
	return prim(sel, s.XGo.Import)
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

// scalarType maps a scalar schema to its Go primitive type.
func scalarType(s *ir.Schema) *TypeRef {
	if len(s.Type) != 1 {
		return prim("any")
	}
	switch s.Type[0] {
	case ir.TypeString:
		return prim("string")
	case ir.TypeInteger:
		return prim("int64")
	case ir.TypeNumber:
		return prim("float64")
	case ir.TypeBoolean:
		return prim("bool")
	default:
		return prim("any")
	}
}

func isObject(s *ir.Schema) bool {
	return len(s.Properties) > 0 || s.Type.Contains(ir.TypeObject)
}

func isArray(s *ir.Schema) bool {
	return s.Items != nil || len(s.PrefixItems) > 0 || s.Type.Contains(ir.TypeArray)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
