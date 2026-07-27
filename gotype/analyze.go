package gotype

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

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
	// Initialisms are additional all-caps words (beyond the built-in set) that
	// generated identifiers render fully uppercase when they form a word, e.g.
	// "PIN", "EMV". Matched case-insensitively.
	Initialisms []string
	// SourceOrder keeps struct fields in the order their properties appeared in
	// the source document (ir.Schema.PropertyOrder) instead of sorting the
	// property keys alphabetically.
	SourceOrder bool
	// ExternalRef, when non-nil, is consulted for every resolvable $ref. If it
	// returns ok, the referenced type is treated as living in another package:
	// the reference becomes a package-qualified TypeRef (pkg selector + import
	// path + Go name) and NO declaration is generated for the target in this
	// model. target is the schema the ref resolves to (after the Resolver has
	// followed any transparent wrappers). Returning ok=false falls back to
	// generating the type in this model.
	ExternalRef func(ref string, target *ir.Schema) (pkg, goName, importPath string, ok bool)
}

// NamedRoot pairs a schema with the Go type name it should be declared under as
// a top-level declaration.
type NamedRoot struct {
	Name   string
	Schema *ir.Schema
}

// Analyze walks a single schema document and produces the Go type model: the
// root and every $defs entry become named, top-level declarations. It is a
// convenience over [AnalyzeRoots].
func Analyze(root *ir.Schema, res Resolver, cfg Config) (*Model, error) {
	rootName := cfg.RootName
	if rootName == "" {
		rootName = "Root"
	}
	roots := []NamedRoot{{Name: rootName, Schema: root}}
	for _, key := range sortedKeys(root.Defs) {
		roots = append(roots, NamedRoot{Name: GoName(key), Schema: root.Defs[key]})
	}
	return AnalyzeRoots(roots, res, cfg)
}

// AnalyzeRoots produces a model from a set of named root schemas, each declared
// as a top-level type under its given name, plus the transitive closure of the
// types they reference. It is the multi-root entry point a driver uses to place
// several unrelated schemas (e.g. a message interface's command/completion/event
// payloads) into one output package.
func AnalyzeRoots(roots []NamedRoot, res Resolver, cfg Config) (*Model, error) {
	if cfg.Package == "" {
		cfg.Package = "schema"
	}
	a := &analyzer{
		cfg:         cfg,
		res:         res,
		model:       &Model{Package: cfg.Package},
		nameByLoc:   map[string]string{},
		declByLoc:   map[string]*Decl{},
		used:        map[string]bool{},
		initialisms: mergeInitialisms(cfg.Initialisms),
	}

	for _, r := range roots {
		a.declare(r.Name, r.Schema)
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
	cfg         Config
	res         Resolver
	model       *Model
	nameByLoc   map[string]string
	declByLoc   map[string]*Decl
	used        map[string]bool
	queue       []*Decl
	initialisms map[string]bool
}

// goName converts a JSON name to a Go identifier using the analyzer's configured
// initialisms in addition to the built-in set.
func (a *analyzer) goName(s string) string { return goNameWith(s, a.initialisms) }

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
	if len(s.AllOf) > 0 && !a.allOfEmbeddable(s) {
		d.AllOfBlocked = a.allOfBlocker(s)
	}
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
		d.Underlying = a.mapType(s, d.Name)
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
		d.Enum = append(d.Enum, EnumVal{Ident: d.Name + a.goName(fmt.Sprint(v)), Value: v})
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
	for _, key := range a.propertyKeys(d.Schema) {
		ps := d.Schema.Properties[key]
		f := &Field{
			Name:     a.goName(key),
			JSONName: key,
			Doc:      ps.Description,
			Type:     a.typeRef(ps, d.Name+a.goName(key)),
			Required: contains(d.Schema.Required, key),
			Schema:   ps,
		}
		// Optional scalars and named types become pointers so absence is
		// distinguishable; types with a natural nil zero value (slices, maps,
		// and x-go []byte / map overrides) do not.
		if !f.Required && !nilable(f.Type) {
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

// embeddableMember reports whether an allOf member can be embedded. It must be
// an object schema, so that its content belongs in the same JSON object as the
// parent's: a struct member contributes its properties, and a dictionary member
// (an object schema with no declared properties, generated as a map) contributes
// its entries. The generated MarshalJSON merges the parts into one flat object,
// so neither shape gets a JSON name of its own.
func embeddableMember(s *ir.Schema) bool {
	return memberBlocker(s) == ""
}

// memberBlocker describes why a member is not embeddable, or "" when it is.
func memberBlocker(s *ir.Schema) string {
	switch {
	case s.IsBoolean():
		return "a boolean schema"
	case xgoType(s) != nil:
		return "an x-go type override"
	case len(s.Enum) > 0:
		return "an enum"
	case s.Const != nil:
		return "a const"
	case len(s.OneOf) > 0 || len(s.AnyOf) > 0:
		return "a oneOf/anyOf union"
	case !isObject(s):
		return "not an object schema"
	}
	return ""
}

// allOfBlocker explains why an allOf cannot be modeled as struct embedding,
// naming the member responsible.
func (a *analyzer) allOfBlocker(s *ir.Schema) string {
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return "the schema's own oneOf/anyOf takes precedence"
	}
	for i, m := range s.AllOf {
		target := m
		if m.Ref != "" {
			r, err := a.res.Resolve(m.BaseURI, m.Ref)
			if err != nil {
				return fmt.Sprintf("member %d: $ref %q does not resolve", i+1, m.Ref)
			}
			target = r
		}
		if why := memberBlocker(target); why != "" {
			return fmt.Sprintf("member %d is %s", i+1, why)
		}
	}
	return ""
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
		return a.refType(s.BaseURI, s.Ref, hint)
	}

	switch {
	case len(s.Enum) > 0 || s.Const != nil:
		return &TypeRef{Named: a.declare(hint, s)}
	case len(s.OneOf) > 0 || len(s.AnyOf) > 0:
		return &TypeRef{Named: a.declare(hint, s)}
	case a.allOfEmbeddable(s):
		return &TypeRef{Named: a.declare(hint, s)}
	case isObject(s) && len(s.Properties) == 0:
		return a.mapType(s, hint)
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

// refType computes the Go type for a $ref. Resolution order:
//  1. If the target carries an x-go type override, inline it (e.g. base64 ->
//     []byte), generating no named alias.
//  2. If Config.ExternalRef claims the target, emit a package-qualified
//     reference and generate no declaration in this model.
//  3. Otherwise declare (or reuse) the target as a named type in this model.
func (a *analyzer) refType(base, ref, hint string) *TypeRef {
	target, err := a.res.Resolve(base, ref)
	if err != nil {
		return prim("any")
	}
	if t := xgoType(target); t != nil {
		return t
	}
	// A plain scalar target (a string/number/integer/boolean with no enum and no
	// structural content) is inlined as its Go scalar rather than named, so only
	// objects and enums become distinct types.
	if isPlainScalar(target) {
		return scalarType(target)
	}
	if a.cfg.ExternalRef != nil {
		if pkg, name, imp, ok := a.cfg.ExternalRef(ref, target); ok {
			return &TypeRef{Named: name, Package: pkg, Import: imp}
		}
	}
	return &TypeRef{Named: a.declare(a.refName(ref, hint), target)}
}

// refName derives a type name from the final segment of a JSON pointer ref.
func (a *analyzer) refName(ref, hint string) string {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '/' {
			return a.goName(ref[i+1:])
		}
	}
	return hint
}

// mapType builds a map[string]T for a dictionary object. The element type T is
// the additionalProperties schema, or — when there is none — the value schema of
// the (deterministically first) patternProperties entry; it is any when neither
// constrains the value. hint names any nested value type generated.
func (a *analyzer) mapType(s *ir.Schema, hint string) *TypeRef {
	elem := prim("any")
	switch {
	case s.AdditionalProperties != nil && !s.AdditionalProperties.AlwaysValid():
		elem = a.typeRef(s.AdditionalProperties, hint+"Value")
	case len(s.PatternProperties) > 0:
		for _, k := range sortedKeys(s.PatternProperties) {
			if ps := s.PatternProperties[k]; ps != nil && !ps.AlwaysValid() {
				elem = a.typeRef(ps, hint+"Value")
			}
			break // one value type; patterns share it
		}
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

// scalarType maps a scalar schema to its Go primitive type. A nullable scalar
// (e.g. ["string","null"]) maps to the underlying scalar; a union of two or more
// non-null types has no single Go scalar and maps to any.
func scalarType(s *ir.Schema) *TypeRef {
	t, ok := soleNonNullType(s.Type)
	if !ok {
		return prim("any")
	}
	switch t {
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

// soleNonNullType returns the single non-null type in ts, if there is exactly
// one (ts may also contain "null").
func soleNonNullType(ts ir.TypeSet) (ir.Type, bool) {
	var out ir.Type
	found := false
	for _, x := range ts {
		if x == ir.TypeNull {
			continue
		}
		if found {
			return 0, false
		}
		out, found = x, true
	}
	return out, found
}

// IsPlainScalar reports whether a $ref to s would be inlined as a Go scalar
// rather than generating a named type — a driver deciding which schemas need a
// top-level declaration can use it to stay consistent with reference handling.
func IsPlainScalar(s *ir.Schema) bool { return isPlainScalar(s) }

// isPlainScalar reports whether s is a bare scalar schema — a string, number,
// integer or boolean (optionally nullable) with no enum, const, $ref, or
// object/array structure — so a $ref to it inlines as the Go scalar rather than
// generating a named type.
func isPlainScalar(s *ir.Schema) bool {
	if s == nil || s.IsBoolean() {
		return false
	}
	if s.Ref != "" || len(s.Enum) > 0 || s.Const != nil {
		return false
	}
	if len(s.Properties) > 0 || len(s.PatternProperties) > 0 || s.AdditionalProperties != nil {
		return false
	}
	if len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 {
		return false
	}
	if s.Items != nil || len(s.PrefixItems) > 0 {
		return false
	}
	t, ok := soleNonNullType(s.Type)
	return ok && t != ir.TypeObject && t != ir.TypeArray && t != ir.TypeNull
}

func isObject(s *ir.Schema) bool {
	return len(s.Properties) > 0 || s.Type.Contains(ir.TypeObject)
}

func isArray(s *ir.Schema) bool {
	return s.Items != nil || len(s.PrefixItems) > 0 || s.Type.Contains(ir.TypeArray)
}

// propertyKeys returns the property names of s in generation order: source order
// when Config.SourceOrder is set and it was captured, otherwise alphabetical.
func (a *analyzer) propertyKeys(s *ir.Schema) []string {
	if a.cfg.SourceOrder && len(s.PropertyOrder) == len(s.Properties) && len(s.PropertyOrder) > 0 {
		return s.PropertyOrder
	}
	return sortedKeys(s.Properties)
}

// nilable reports whether a type already has a nil zero value, so an optional
// field of this type needs no pointer wrapper (slices, maps, and x-go []byte /
// map[...] overrides).
func nilable(t *TypeRef) bool {
	if t.Slice != nil || t.Map != nil {
		return true
	}
	return strings.HasPrefix(t.Prim, "[]") || strings.HasPrefix(t.Prim, "map[")
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
