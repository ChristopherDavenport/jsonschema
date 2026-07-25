package gen

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/dave/jennifer/jen"

	"github.com/ChristopherDavenport/jsonschema/gotype"
	"github.com/ChristopherDavenport/jsonschema/ir"
	"github.com/ChristopherDavenport/jsonschema/xvalid"
)

const xvalidPkg = "github.com/ChristopherDavenport/jsonschema/xvalid"

type emitter struct {
	cfg         Config
	model       *gotype.Model
	docBytes    []byte
	hasValidate map[string]bool
	isInterface map[string]bool
	declByName  map[string]*gotype.Decl
	patterns    []patternVar
	needEngine  bool // an engine-fallback helper block must be emitted
}

const enginePkg = "github.com/ChristopherDavenport/jsonschema"

// hybrid reports whether a declaration's Validate should delegate to the
// runtime engine rather than mirror the schema inline.
func (e *emitter) hybrid(d *gotype.Decl) bool {
	return e.cfg.EngineFallback && e.needsFallback(d)
}

// needsFallback reports whether a declaration uses keywords that are neither
// mirrored inline nor handled idiomatically (allOf embedding, discriminator if).
func (e *emitter) needsFallback(d *gotype.Decl) bool {
	return len(e.unenforced(d)) > 0
}

// unenforcedItem is one keyword a declaration uses that generated code does not
// enforce, together with whether -engine-fallback would actually fix it.
type unenforcedItem struct {
	keyword string   // "not", "dependentSchemas", "if/then/else", "allOf"
	detail  string   // the full line: keyword plus which rule it missed
	unseen  []string // properties it constrains that this type does not declare
}

// engineCanEnforce reports whether a delegating Validate would enforce this item
// faithfully. It cannot when the keyword constrains properties the Go type does
// not declare: the fallback validates the marshaled value, where such a property
// is always absent — so the check either always passes or always fails.
func (u unenforcedItem) engineCanEnforce() bool { return len(u.unseen) == 0 }

// unenforced lists the keywords this declaration uses that generated code does
// not enforce, naming for each the rule it missed and whether the engine
// fallback covers it. An empty result means everything is mirrored.
func (e *emitter) unenforced(d *gotype.Decl) []unenforcedItem {
	s := d.Schema
	if s == nil {
		return nil
	}
	declared := e.declaredJSON(d)
	var out []unenforcedItem
	add := func(keyword, detail string, constrains ...*ir.Schema) {
		out = append(out, unenforcedItem{
			keyword: keyword,
			detail:  detail,
			unseen:  undeclaredProps(declared, constrains...),
		})
	}

	if s.Not != nil {
		add("not", "not", s.Not)
	}
	if len(s.DependentSchemas) > 0 {
		triggers := sortedStrings(keysOf(s.DependentSchemas))
		item := unenforcedItem{
			keyword: "dependentSchemas",
			detail:  "dependentSchemas (" + quoteList(triggers) + ")",
		}
		// A trigger property the type does not declare can never look present.
		for _, t := range triggers {
			if !declared[t] {
				item.unseen = append(item.unseen, t)
			}
		}
		deps := make([]*ir.Schema, 0, len(s.DependentSchemas))
		for _, t := range triggers {
			deps = append(deps, s.DependentSchemas[t])
		}
		item.unseen = mergeSorted(item.unseen, undeclaredProps(declared, deps...))
		out = append(out, item)
	}
	if s.If != nil || s.Then != nil || s.Else != nil {
		if why := e.ifBlocker(d); why != "" {
			add("if/then/else", "if/then/else: "+why, s.If, s.Then, s.Else)
		}
	}
	if len(s.AllOf) > 0 && !d.AllOfHandled {
		why := d.AllOfBlocked
		if why == "" {
			why = "members cannot be expressed as struct embedding"
		}
		add("allOf", "allOf: "+why, s.AllOf...)
	}
	return out
}

// declaredJSON is the set of JSON property names this type can hold: its own
// fields plus those promoted from embedded allOf parts. A name outside this set
// does not survive a marshal round trip, so the engine fallback cannot see it.
func (e *emitter) declaredJSON(d *gotype.Decl) map[string]bool {
	declared := map[string]bool{}
	var walk func(*gotype.Decl, int)
	walk = func(d *gotype.Decl, depth int) {
		if d == nil || depth > 8 {
			return
		}
		for _, f := range d.Fields {
			declared[f.JSONName] = true
		}
		for _, emb := range d.Embeds {
			walk(e.declByName[emb.Named], depth+1)
		}
	}
	walk(d, 0)
	return declared
}

// undeclaredProps collects the property names the given subschemas constrain on
// *this* instance — via required, properties, and the dependent keywords — that
// are not in the declared set. It descends only through in-place applicators,
// which apply to the same instance; a property subschema constrains a child
// value, whose own properties belong to a different Go type.
func undeclaredProps(declared map[string]bool, schemas ...*ir.Schema) []string {
	seen := map[string]bool{}
	var walk func(*ir.Schema, int)
	walk = func(s *ir.Schema, depth int) {
		if s == nil || s.IsBoolean() || depth > 8 {
			return
		}
		names := append([]string{}, s.Required...)
		names = append(names, keysOf(s.Properties)...)
		names = append(names, keysOf(s.DependentRequired)...)
		names = append(names, keysOf(s.DependentSchemas)...)
		for _, n := range names {
			if !declared[n] {
				seen[n] = true
			}
		}
		for _, in := range append(append([]*ir.Schema{}, s.AllOf...), append(s.AnyOf, s.OneOf...)...) {
			walk(in, depth+1)
		}
		walk(s.Not, depth+1)
		walk(s.If, depth+1)
		walk(s.Then, depth+1)
		walk(s.Else, depth+1)
		for _, dep := range s.DependentSchemas {
			walk(dep, depth+1)
		}
	}
	for _, s := range schemas {
		walk(s, 0)
	}
	return sortedStrings(keysOf(seen))
}

// mergeSorted unions two name lists.
func mergeSorted(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range append(a, b...) {
		set[s] = true
	}
	return sortedStrings(keysOf(set))
}

// quoteList renders names as a comma-separated list of quoted strings.
func quoteList(names []string) string {
	var b bytes.Buffer
	for i, n := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", n)
	}
	return b.String()
}

type patternVar struct {
	name    string
	pattern string
}

func (e *emitter) emit() ([]byte, error) {
	f := jen.NewFile(e.model.Package)
	f.HeaderComment("Code generated by jsonschema-gen; DO NOT EDIT.")

	// Classify declarations: which have Validate methods, which are interfaces.
	for _, d := range e.model.Decls {
		e.declByName[d.Name] = d
		if d.Kind == gotype.Struct || d.Kind == gotype.Enum {
			e.hasValidate[d.Name] = true
		}
		if d.Kind == gotype.Interface {
			e.isInterface[d.Name] = true
		}
	}

	for _, d := range e.model.Decls {
		switch d.Kind {
		case gotype.Struct:
			e.emitStruct(f, d)
		case gotype.Enum:
			e.emitEnum(f, d)
		case gotype.Alias:
			e.emitAlias(f, d)
		case gotype.Interface:
			e.emitInterface(f, d)
		}
	}

	for _, p := range e.patterns {
		f.Var().Id(p.name).Op("=").Qual(xvalidPkg, "MustCompilePattern").Call(jen.Lit(p.pattern))
	}

	if e.needEngine {
		e.emitEngineHelpers(f)
	}

	var buf bytes.Buffer
	if err := f.Render(&buf); err != nil {
		return nil, fmt.Errorf("gen: render: %w", err)
	}
	return buf.Bytes(), nil
}

func (e *emitter) emitStruct(f *jen.File, d *gotype.Decl) {
	f.Comment(typeDoc(d.Name, d.Doc))
	fields := make([]jen.Code, 0, len(d.Fields)+len(d.Embeds))
	for _, emb := range d.Embeds {
		fields = append(fields, jen.Id(emb.Named)) // embedded (anonymous) field
	}
	for _, fld := range d.Fields {
		var code *jen.Statement
		if d.Tuple {
			// Tuple positions carry no JSON object tags.
			code = jen.Id(fld.Name).Add(e.fieldType(fld))
		} else {
			tag := fld.JSONName
			if !fld.Required {
				tag += ",omitempty"
			}
			tags := map[string]string{"json": tag}
			for _, extra := range fld.ExtraTags {
				if k, v, ok := splitTag(extra); ok {
					tags[k] = v
				}
			}
			code = jen.Id(fld.Name).Add(e.fieldType(fld)).Tag(tags)
		}
		if fld.Doc != "" {
			code = jen.Comment(fld.Name + " " + fld.Doc).Line().Add(code)
		}
		fields = append(fields, code)
	}
	f.Type().Id(d.Name).Struct(fields...)

	if d.Tuple {
		e.emitTupleMarshal(f, d)
	} else {
		e.emitUnmarshal(f, d)
	}
	e.emitStructValidate(f, d)
}

// emitTupleMarshal generates array-shaped (Un)MarshalJSON for a positional
// tuple: each element maps to the JSON array position of the same index.
func (e *emitter) emitTupleMarshal(f *jen.File, d *gotype.Decl) {
	n := len(d.Fields)
	body := []jen.Code{
		jen.Var().Id("a").Index().Qual("encoding/json", "RawMessage"),
		jen.If(jen.Err().Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("a")), jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())),
		jen.If(jen.Len(jen.Id("a")).Op("<").Lit(n)).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit(d.Name+": expected at least "+strconv.Itoa(n)+" elements, got %d"), jen.Len(jen.Id("a")))),
		),
	}
	for i, fld := range d.Fields {
		body = append(body, jen.If(
			jen.Err().Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("a").Index(jen.Lit(i)), jen.Op("&").Id("x").Dot(fld.Name)),
			jen.Err().Op("!=").Nil(),
		).Block(jen.Return(jen.Err())))
	}
	body = append(body, jen.Return(jen.Nil()))
	f.Comment("UnmarshalJSON decodes a JSON array into " + d.Name + "'s positional elements.")
	f.Func().Params(jen.Id("x").Op("*").Id(d.Name)).Id("UnmarshalJSON").Params(jen.Id("data").Index().Byte()).Error().Block(body...)

	elems := make([]jen.Code, n)
	for i, fld := range d.Fields {
		elems[i] = jen.Id("x").Dot(fld.Name)
	}
	f.Comment("MarshalJSON encodes " + d.Name + " as a JSON array.")
	f.Func().Params(jen.Id("x").Id(d.Name)).Id("MarshalJSON").Params().Params(jen.Index().Byte(), jen.Error()).Block(
		jen.Return(jen.Qual("encoding/json", "Marshal").Call(jen.Index().Any().Values(elems...))),
	)
}

// emitUnmarshal generates an UnmarshalJSON when the struct needs one: to enforce
// required properties at decode time, and/or to dispatch interface-typed fields
// (which encoding/json cannot populate on its own) through their Unmarshal<T>.
func (e *emitter) emitUnmarshal(f *jen.File, d *gotype.Decl) {
	var required []string
	var ifaceFields []*gotype.Field
	for _, fld := range d.Fields {
		if fld.Required {
			required = append(required, fld.JSONName)
		}
		if name := coreName(fld.Type); name != "" && e.isInterface[name] {
			ifaceFields = append(ifaceFields, fld)
		}
	}
	if len(required) == 0 && len(ifaceFields) == 0 && len(d.Embeds) == 0 {
		return
	}

	var body []jen.Code
	body = append(body,
		jen.Var().Id("raw").Map(jen.String()).Qual("encoding/json", "RawMessage"),
		jen.If(jen.Err().Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("raw")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Err()),
		),
	)
	// allOf: decode each embedded part from the full object (each enforces its
	// own required fields and fills its own promoted fields).
	for _, emb := range d.Embeds {
		body = append(body, jen.If(
			jen.Err().Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("x").Dot(emb.Named)),
			jen.Err().Op("!=").Nil(),
		).Block(jen.Return(jen.Err())))
	}
	if len(required) > 0 {
		lits := make([]jen.Code, len(required))
		for i, r := range required {
			lits[i] = jen.Lit(r)
		}
		body = append(body, jen.For(jen.List(jen.Id("_"), jen.Id("k")).Op(":=").Range().Index().String().Values(lits...)).Block(
			jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("raw").Index(jen.Id("k")), jen.Op("!").Id("ok")).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("missing required property %q"), jen.Id("k"))),
			),
		))
	}

	// Shadow struct: decode this type's own (non-embedded) fields. Interface
	// fields become raw JSON; everything else keeps its real type.
	if len(d.Fields) > 0 {
		shadowFields := make([]jen.Code, 0, len(d.Fields))
		for _, fld := range d.Fields {
			tag := fld.JSONName
			if !fld.Required {
				tag += ",omitempty"
			}
			var ft *jen.Statement
			if name := coreName(fld.Type); name != "" && e.isInterface[name] {
				ft = jen.Qual("encoding/json", "RawMessage")
			} else {
				ft = e.fieldType(fld)
			}
			shadowFields = append(shadowFields, jen.Id(fld.Name).Add(ft).Tag(map[string]string{"json": tag}))
		}
		body = append(body,
			jen.Type().Id("shadow").Struct(shadowFields...),
			jen.Var().Id("sh").Id("shadow"),
			jen.If(jen.Err().Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("sh")), jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Err()),
			),
		)

		ifaceSet := map[string]bool{}
		for _, fld := range ifaceFields {
			ifaceSet[fld.Name] = true
		}
		for _, fld := range d.Fields {
			if ifaceSet[fld.Name] {
				body = append(body, jen.If(jen.Len(jen.Id("sh").Dot(fld.Name)).Op(">").Lit(0)).Block(
					jen.List(jen.Id("v"), jen.Err()).Op(":=").Id("Unmarshal"+coreName(fld.Type)).Call(jen.Id("sh").Dot(fld.Name)),
					jen.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())),
					jen.Id("x").Dot(fld.Name).Op("=").Id("v"),
				))
			} else {
				body = append(body, jen.Id("x").Dot(fld.Name).Op("=").Id("sh").Dot(fld.Name))
			}
		}
	}
	body = append(body, jen.Return(jen.Nil()))

	f.Comment("UnmarshalJSON decodes data into " + d.Name + ", enforcing required properties.")
	f.Func().Params(jen.Id("x").Op("*").Id(d.Name)).Id("UnmarshalJSON").
		Params(jen.Id("data").Index().Byte()).Error().Block(body...)
}

// fieldType is the Go type of a struct field: interface-typed fields are the
// interface itself (never a pointer), everything else uses typeCode.
func (e *emitter) fieldType(fld *gotype.Field) *jen.Statement {
	if name := coreName(fld.Type); name != "" && e.isInterface[name] {
		return jen.Id(name)
	}
	return e.typeCode(fld.Type)
}

// coreName returns the named type a TypeRef refers to (ignoring a pointer), or
// "" if it is not a direct named reference.
func coreName(t *gotype.TypeRef) string { return t.Named }

// typeDoc builds a godoc comment for a generated type: the schema description
// when present, and a sensible default otherwise, always leading with the name.
func typeDoc(name, desc string) string {
	if desc != "" {
		return name + " " + desc
	}
	return name + " is generated from its JSON Schema."
}

// emitTypeDoc writes a declaration's godoc comment. For a type that is not a
// struct — an alias, enum or union interface — there is no mirrored Validate to
// carry the NOTE, and the engine fallback cannot delegate either, so the warning
// about unenforced keywords goes on the type itself.
func (e *emitter) emitTypeDoc(f *jen.File, d *gotype.Decl, extra string) {
	f.Comment(typeDoc(d.Name, d.Doc) + extra)
	items := e.unenforced(d)
	if len(items) == 0 {
		return
	}
	f.Comment("")
	f.Comment("NOTE: nothing generated for this type enforces:")
	for _, it := range items {
		f.Comment("  - " + it.detail)
	}
	f.Comment("")
	// The fallback rewrites Validate methods, and this type has none, so the
	// flag is never the answer here whatever the keywords are.
	f.Comment("-engine-fallback cannot enforce these: it rewrites Validate methods, and")
	f.Comment("this type has none. Validate values of this type against the schema with")
	f.Comment("the jsonschema engine.")
}

// remedyLines is the tail of a struct's NOTE: what the reader should do. It
// distinguishes the keywords -engine-fallback would enforce from those it could
// not, because the fallback validates the marshaled Go value and a property this
// type does not declare is always absent from it — so such a check would always
// pass, or always fail, rather than mirror the schema.
func remedyLines(items []unenforcedItem) []string {
	var fixable, stuck []string
	var unseen []string
	for _, it := range items {
		if it.engineCanEnforce() {
			fixable = append(fixable, it.keyword)
			continue
		}
		stuck = append(stuck, it.keyword)
		unseen = mergeSorted(unseen, it.unseen)
	}

	switch {
	case len(stuck) == 0:
		return wrapComment("Regenerate with -engine-fallback, or validate with the jsonschema engine.")
	case len(fixable) == 0:
		return wrapComment("-engine-fallback cannot enforce " + joinList(stuck) + " here: it validates the marshaled value of this type, which never carries " +
			quoteList(unseen) + ". Validate the original document with the jsonschema engine instead.")
	default:
		return wrapComment("Regenerate with -engine-fallback to enforce " + joinList(fixable) +
			". It cannot enforce " + joinList(stuck) + ", which constrains " + quoteList(unseen) +
			": that is absent from the marshaled value of this type, so validate the original document with the jsonschema engine.")
	}
}

// joinList renders quoted keywords as `"a"`, `"a" and "b"`, or `"a", "b" and
// "c"`. Quoting keeps a bare `not` from reading as English negation.
func joinList(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}

// wrapComment breaks text into comment-width lines on word boundaries.
func wrapComment(text string) []string {
	const width = 74
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func keysOf[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func sortedStrings(ss []string) []string {
	sort.Strings(ss)
	return ss
}

// splitTag splits an "key:value" extra tag into its parts.
func splitTag(s string) (key, value string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func (e *emitter) emitStructValidate(f *jen.File, d *gotype.Decl) {
	if e.hybrid(d) {
		e.emitDelegatingValidate(f, d)
		return
	}
	var body []jen.Code
	// allOf: validate each embedded part.
	for _, emb := range d.Embeds {
		if e.hasValidate[emb.Named] {
			body = append(body, jen.If(jen.Err().Op(":=").Id("x").Dot(emb.Named).Dot("Validate").Call(), jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())))
		}
	}
	for _, fld := range d.Fields {
		checks := e.fieldChecks(fld)
		if len(checks) == 0 {
			continue
		}
		if fld.Type.Pointer {
			body = append(body, jen.If(jen.Id("x").Dot(fld.Name).Op("!=").Nil()).Block(checks...))
		} else {
			body = append(body, checks...)
		}
	}
	body = append(body, e.dependentRequiredChecks(d)...)
	if e.ifHandled(d) {
		body = append(body, e.emitIfThenElse(d)...)
	}
	body = append(body, jen.Return(jen.Nil()))

	// Warn when the schema uses keywords the generator does not enforce (and
	// the engine fallback was not requested), naming each one and saying which
	// of them regenerating with -engine-fallback would actually fix.
	if items := e.unenforced(d); len(items) > 0 {
		f.Comment("Validate reports whether x satisfies the constraints this type mirrors inline.")
		f.Comment("")
		f.Comment("NOTE: it does not enforce:")
		for _, it := range items {
			f.Comment("  - " + it.detail)
		}
		f.Comment("")
		for _, line := range remedyLines(items) {
			f.Comment(line)
		}
	} else {
		f.Comment("Validate reports whether x satisfies the schema.")
	}
	f.Func().Params(jen.Id("x").Op("*").Id(d.Name)).Id("Validate").Params().Error().Block(body...)
}

// ifHandled reports whether a struct's if/then/else is a string-discriminator
// with required-only then/else branches — the subset we mirror idiomatically.
// It is defined as "ifBlocker found nothing", so the decision to mirror and the
// explanation emitted when we don't can never disagree.
func (e *emitter) ifHandled(d *gotype.Decl) bool {
	return e.ifBlocker(d) == ""
}

// ifBlocker names the first recognition rule a struct's if/then/else fails, in
// schema terms, or "" when the whole shape is mirrored inline. The rules: the
// `if` constrains nothing but `properties`; every one of those properties is a
// plain string const/enum match against a plain Go string field; and `then`/
// `else` constrain nothing but a non-empty `required` over declared properties.
func (e *emitter) ifBlocker(d *gotype.Decl) string {
	s := d.Schema
	switch {
	case s.If == nil:
		return "then/else with no if"
	case s.If.IsBoolean():
		return "if is a boolean schema"
	case len(s.If.Properties) == 0:
		return "if does not constrain any property"
	case s.If.Ref != "" || s.If.Not != nil || s.If.If != nil ||
		len(s.If.AllOf) > 0 || len(s.If.AnyOf) > 0 || len(s.If.OneOf) > 0 ||
		len(s.If.Required) > 0 || !s.If.Type.Empty():
		return "if constrains more than properties"
	}

	byJSON := fieldsByJSON(d)
	for _, name := range sortedStrings(keysOf(s.If.Properties)) {
		if !isSimpleStringMatch(s.If.Properties[name]) {
			return fmt.Sprintf("if property %q is not a plain string const/enum match", name)
		}
		f, ok := byJSON[name]
		if !ok {
			return fmt.Sprintf("if property %q is not a declared property", name)
		}
		if !isStringField(f) {
			return fmt.Sprintf("if property %q is not a plain Go string field", name)
		}
	}

	for _, branch := range []struct {
		kw  string
		sch *ir.Schema
	}{{"then", s.Then}, {"else", s.Else}} {
		if branch.sch == nil {
			continue
		}
		if !isRequiredOnly(branch.sch) {
			return branch.kw + " constrains more than a non-empty required"
		}
		// A required name with no field could not be checked at all, and
		// pretending otherwise would emit a Validate that silently ignores it.
		for _, name := range branch.sch.Required {
			if _, ok := byJSON[name]; !ok {
				return fmt.Sprintf("%s requires %q, which is not a declared property", branch.kw, name)
			}
		}
	}
	return ""
}

// isSimpleStringMatch reports whether ps matches a string value and nothing
// else: a string `const` or an all-string `enum`, optionally restating `"type":
// "string"`. Any further assertion on the tag would have to be mirrored too, so
// it disqualifies the discriminator shape rather than being dropped.
func isSimpleStringMatch(ps *ir.Schema) bool {
	if ps.IsBoolean() || stringDiscriminatorValues(ps) == nil {
		return false
	}
	if ps.Const != nil && len(ps.Enum) > 0 {
		return false // both apply; only const would be mirrored
	}
	// `type` may only restate that the value is a string.
	if !ps.Type.Empty() && (len(ps.Type) != 1 || !ps.Type.Contains(ir.TypeString)) {
		return false
	}
	return !hasOtherAssertions(ps)
}

// hasOtherAssertions reports whether s carries any assertion or applicator
// beyond `type`, `const` and `enum`. Pure annotations (title, description,
// default, examples, deprecated, $comment) are ignored: they never constrain.
func hasOtherAssertions(s *ir.Schema) bool {
	switch {
	case s.Ref != "" || s.DynamicRef != "":
	case len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.OneOf) > 0 || s.Not != nil:
	case s.If != nil || s.Then != nil || s.Else != nil || len(s.DependentSchemas) > 0:
	case len(s.Properties) > 0 || len(s.PatternProperties) > 0 ||
		s.AdditionalProperties != nil || s.PropertyNames != nil || s.UnevaluatedProperties != nil:
	case len(s.PrefixItems) > 0 || s.Items != nil || s.Contains != nil || s.UnevaluatedItems != nil:
	case s.MultipleOf != nil || s.Maximum != nil || s.ExclusiveMaximum != nil ||
		s.Minimum != nil || s.ExclusiveMinimum != nil:
	case s.MaxLength != nil || s.MinLength != nil || s.Pattern != "":
	case s.MaxItems != nil || s.MinItems != nil || s.UniqueItems ||
		s.MaxContains != nil || s.MinContains != nil:
	case s.MaxProperties != nil || s.MinProperties != nil ||
		len(s.Required) > 0 || len(s.DependentRequired) > 0:
	case s.Format != "":
	case s.ContentEncoding != "" || s.ContentMediaType != "" || s.ContentSchema != nil:
	default:
		return false
	}
	return true
}

// stringDiscriminatorValues returns the string values a property must equal
// (from const or enum), or nil if it is not a simple string match.
func stringDiscriminatorValues(ps *ir.Schema) []string {
	if ps.Const != nil {
		if s, ok := (*ps.Const).(string); ok {
			return []string{s}
		}
		return nil
	}
	if len(ps.Enum) > 0 {
		vals := make([]string, 0, len(ps.Enum))
		for _, v := range ps.Enum {
			s, ok := v.(string)
			if !ok {
				return nil
			}
			vals = append(vals, s)
		}
		return vals
	}
	return nil
}

// isRequiredOnly reports whether sch constrains nothing but `required`.
func isRequiredOnly(sch *ir.Schema) bool {
	if sch.IsBoolean() || len(sch.Required) == 0 {
		return false
	}
	return len(sch.Properties) == 0 && sch.If == nil && sch.Not == nil &&
		len(sch.AllOf) == 0 && len(sch.AnyOf) == 0 && len(sch.OneOf) == 0
}

func isStringField(f *gotype.Field) bool {
	return f.Type.Named == "" && f.Type.Slice == nil && f.Type.Map == nil && f.Type.Prim == "string"
}

func fieldsByJSON(d *gotype.Decl) map[string]*gotype.Field {
	m := make(map[string]*gotype.Field, len(d.Fields))
	for _, f := range d.Fields {
		m[f.JSONName] = f
	}
	return m
}

// emitIfThenElse emits the discriminator branch: if the tag fields match, the
// `then` required properties apply; otherwise the `else` required properties do.
func (e *emitter) emitIfThenElse(d *gotype.Decl) []jen.Code {
	s := d.Schema
	byJSON := fieldsByJSON(d)

	var cond *jen.Statement
	for _, name := range sortedStrings(keysOf(s.If.Properties)) {
		term := discriminatorTerm(byJSON[name], stringDiscriminatorValues(s.If.Properties[name]))
		if cond == nil {
			cond = term
		} else {
			cond = cond.Op("&&").Add(term)
		}
	}

	thenChecks := requiredPresenceChecks(byJSON, s.Then)
	if len(thenChecks) == 0 {
		thenChecks = []jen.Code{jen.Comment("no additional requirements")}
	}
	stmt := jen.If(cond).Block(thenChecks...)
	if elseChecks := requiredPresenceChecks(byJSON, s.Else); len(elseChecks) > 0 {
		stmt = stmt.Else().Block(elseChecks...)
	}
	return []jen.Code{stmt}
}

// discriminatorTerm builds `(x.F == nil || *x.F == v1 || ...)` for a pointer
// field, or `(x.F == v1 || ...)` for a required one.
func discriminatorTerm(fld *gotype.Field, vals []string) *jen.Statement {
	value := func() *jen.Statement {
		if fld.Type.Pointer {
			return jen.Op("*").Id("x").Dot(fld.Name)
		}
		return jen.Id("x").Dot(fld.Name)
	}
	var parts []*jen.Statement
	if fld.Type.Pointer {
		parts = append(parts, jen.Id("x").Dot(fld.Name).Op("==").Nil())
	}
	for _, v := range vals {
		parts = append(parts, value().Op("==").Lit(v))
	}
	expr := parts[0]
	for _, p := range parts[1:] {
		expr = expr.Op("||").Add(p)
	}
	return jen.Parens(expr)
}

// requiredPresenceChecks emits a presence check per required property of sch.
func requiredPresenceChecks(byJSON map[string]*gotype.Field, sch *ir.Schema) []jen.Code {
	if sch == nil {
		return nil
	}
	var out []jen.Code
	for _, name := range sch.Required {
		f, ok := byJSON[name]
		if !ok {
			continue
		}
		if missing := absentExpr(f); missing != nil {
			out = append(out, jen.If(missing).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit(fmt.Sprintf("%q is required here", name)))),
			))
		}
	}
	return out
}

// emitDelegatingValidate emits a Validate that defers to the runtime engine,
// validating the marshaled value against this type's subschema by location.
// Delegation is faithful only for keywords that constrain properties this type
// declares, so any that do not are called out here too.
func (e *emitter) emitDelegatingValidate(f *jen.File, d *gotype.Decl) {
	e.needEngine = true

	var stuck []string
	var unseen []string
	for _, it := range e.unenforced(d) {
		if !it.engineCanEnforce() {
			stuck = append(stuck, it.keyword)
			unseen = mergeSorted(unseen, it.unseen)
		}
	}

	f.Comment("Validate reports whether x satisfies the schema, delegating to the")
	f.Comment("embedded schema and runtime engine for full conformance.")
	if len(stuck) > 0 {
		verb := "is"
		if len(stuck) > 1 {
			verb = "are"
		}
		f.Comment("")
		for _, line := range wrapComment("NOTE: " + joinList(stuck) + " " + verb +
			" not enforced faithfully even here: the engine sees the marshaled value of this type, which never carries " +
			quoteList(unseen) + ". Validate the original document with the jsonschema engine.") {
			f.Comment(line)
		}
	}
	f.Func().Params(jen.Id("x").Op("*").Id(d.Name)).Id("Validate").Params().Error().Block(
		jen.Return(jen.Id("validateAgainstSchema").Call(jen.Lit(d.Schema.Location), jen.Id("x"))),
	)
}

// emitEngineHelpers emits the shared block that compiles the embedded schema
// once and validates a marshaled value against a subschema by location.
func (e *emitter) emitEngineHelpers(f *jen.File) {
	f.Comment("The embedded schema and engine below back the Validate methods of")
	f.Comment("types that use keywords not mirrored inline (if/then/else,")
	f.Comment("dependentSchemas, not, allOf).")
	f.Const().Id("schemaBaseURI").Op("=").Lit(e.cfg.BaseURI)
	f.Var().Id("schemaDocument").Op("=").Lit(string(e.docBytes))

	compilerBody := []jen.Code{jen.Id("c").Op(":=").Qual(enginePkg, "NewCompiler").Call()}
	if e.cfg.AssertFormat {
		compilerBody = append(compilerBody, jen.Id("c").Dot("AssertFormat").Call(jen.True()))
	}
	compilerBody = append(compilerBody,
		jen.Id("_").Op("=").Id("c").Dot("AddResource").Call(jen.Id("schemaBaseURI"), jen.Index().Byte().Call(jen.Id("schemaDocument"))),
		jen.Return(jen.Id("c")),
	)
	f.Var().Id("schemaCompiler").Op("=").Qual("sync", "OnceValue").Call(
		jen.Func().Params().Op("*").Qual(enginePkg, "Compiler").Block(compilerBody...),
	)

	f.Func().Id("validateAgainstSchema").Params(jen.Id("location").String(), jen.Id("v").Any()).Error().Block(
		jen.List(jen.Id("s"), jen.Err()).Op(":=").Id("schemaCompiler").Call().Dot("Compile").Call(jen.Id("location")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())),
		jen.List(jen.Id("b"), jen.Err()).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("v")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())),
		jen.Id("dec").Op(":=").Qual("encoding/json", "NewDecoder").Call(jen.Qual("bytes", "NewReader").Call(jen.Id("b"))),
		jen.Id("dec").Dot("UseNumber").Call(),
		jen.Var().Id("decoded").Any(),
		jen.If(jen.Err().Op(":=").Id("dec").Dot("Decode").Call(jen.Op("&").Id("decoded")), jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())),
		jen.Return(jen.Id("s").Dot("Validate").Call(jen.Id("decoded"))),
	)
}

// dependentRequiredChecks emits, for each trigger property that is present, a
// check that its dependent properties are also present.
func (e *emitter) dependentRequiredChecks(d *gotype.Decl) []jen.Code {
	if len(d.Schema.DependentRequired) == 0 {
		return nil
	}
	byJSON := map[string]*gotype.Field{}
	for _, fld := range d.Fields {
		byJSON[fld.JSONName] = fld
	}

	var out []jen.Code
	for _, name := range sortedStrings(keysOf(d.Schema.DependentRequired)) {
		trigger := byJSON[name]
		if trigger == nil {
			continue
		}
		var checks []jen.Code
		for _, dep := range d.Schema.DependentRequired[name] {
			df := byJSON[dep]
			if df == nil {
				continue
			}
			missing := absentExpr(df)
			if missing == nil {
				continue // a required (always-present) field is never missing
			}
			checks = append(checks, jen.If(missing).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit(fmt.Sprintf("%q requires %q", name, dep)))),
			))
		}
		if len(checks) == 0 {
			continue
		}
		if present := presentExpr(trigger); present != nil {
			out = append(out, jen.If(present).Block(checks...))
		} else {
			out = append(out, checks...) // trigger always present
		}
	}
	return out
}

// presentExpr returns the condition under which a field is present, or nil when
// the field is always present (a required, non-pointer scalar).
func presentExpr(f *gotype.Field) *jen.Statement {
	if f.Type.Pointer || f.Type.Slice != nil || f.Type.Map != nil {
		return jen.Id("x").Dot(f.Name).Op("!=").Nil()
	}
	return nil
}

// absentExpr is the negation of presentExpr, or nil when never absent.
func absentExpr(f *gotype.Field) *jen.Statement {
	if f.Type.Pointer || f.Type.Slice != nil || f.Type.Map != nil {
		return jen.Id("x").Dot(f.Name).Op("==").Nil()
	}
	return nil
}

// fieldChecks returns the validation statements for one field. The accessor is
// dereferenced when the field is a pointer (guarded by the caller).
func (e *emitter) fieldChecks(fld *gotype.Field) []jen.Code {
	val := func() *jen.Statement {
		if fld.Type.Pointer {
			return jen.Op("*").Id("x").Dot(fld.Name)
		}
		return jen.Id("x").Dot(fld.Name)
	}
	s := fld.Schema
	var out []jen.Code
	errf := func(msg string) jen.Code {
		return jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit(fld.JSONName + ": " + msg)))
	}

	// Interface-typed field: validate the concrete variant if it can.
	if name := coreName(fld.Type); name != "" && e.isInterface[name] {
		return []jen.Code{jen.If(jen.Id("x").Dot(fld.Name).Op("!=").Nil()).Block(
			jen.If(
				jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id("x").Dot(fld.Name).Assert(jen.Interface(jen.Id("Validate").Params().Error())),
				jen.Id("ok"),
			).Block(
				jen.If(jen.Err().Op(":=").Id("v").Dot("Validate").Call(), jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())),
			),
		)}
	}

	// String assertions.
	if s.MinLength != nil {
		out = append(out, jen.If(jen.Qual("unicode/utf8", "RuneCountInString").Call(val()).Op("<").Lit(int(*s.MinLength))).Block(errf("too short")))
	}
	if s.MaxLength != nil {
		out = append(out, jen.If(jen.Qual("unicode/utf8", "RuneCountInString").Call(val()).Op(">").Lit(int(*s.MaxLength))).Block(errf("too long")))
	}
	if s.Pattern != "" {
		if _, err := xvalid.CompilePattern(s.Pattern); err == nil {
			name := e.patternVarFor(s.Pattern)
			out = append(out, jen.If(jen.Op("!").Id(name).Dot("MatchString").Call(val())).Block(errf("does not match pattern")))
		}
	}
	if s.Format != "" && e.cfg.AssertFormat {
		out = append(out, jen.If(
			jen.List(jen.Id("ok"), jen.Id("_")).Op(":=").Qual(xvalidPkg, "CheckFormat").Call(jen.Lit(s.Format), val()),
			jen.Op("!").Id("ok"),
		).Block(errf("invalid "+s.Format)))
	}

	// Numeric assertions.
	e.numericChecks(&out, s, fieldNumKind(fld), val, errf)

	// Array assertions.
	if s.MinItems != nil {
		out = append(out, jen.If(jen.Len(val()).Op("<").Lit(int(*s.MinItems))).Block(errf("too few items")))
	}
	if s.MaxItems != nil {
		out = append(out, jen.If(jen.Len(val()).Op(">").Lit(int(*s.MaxItems))).Block(errf("too many items")))
	}
	if s.UniqueItems {
		out = append(out, jen.Block(
			jen.Id("arr").Op(":=").Add(val()),
			jen.For(jen.Id("i").Op(":=").Lit(0), jen.Id("i").Op("<").Len(jen.Id("arr")), jen.Id("i").Op("++")).Block(
				jen.For(jen.Id("j").Op(":=").Id("i").Op("+").Lit(1), jen.Id("j").Op("<").Len(jen.Id("arr")), jen.Id("j").Op("++")).Block(
					jen.If(jen.Qual("reflect", "DeepEqual").Call(jen.Id("arr").Index(jen.Id("i")), jen.Id("arr").Index(jen.Id("j")))).Block(errf("items are not unique")),
				),
			),
		))
	}

	// Nested validation for named element/field types.
	if fld.Type.Named != "" && e.hasValidate[fld.Type.Named] {
		out = append(out, jen.If(jen.Err().Op(":=").Add(accessorForValidate(fld)).Dot("Validate").Call(), jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())))
	} else if fld.Type.Slice != nil && fld.Type.Slice.Named != "" && e.hasValidate[fld.Type.Slice.Named] {
		out = append(out, jen.For(jen.List(jen.Id("_"), jen.Id("it")).Op(":=").Range().Add(val())).Block(
			jen.If(jen.Err().Op(":=").Id("it").Dot("Validate").Call(), jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())),
		))
	}

	return out
}

// numericChecks emits numeric bound and multipleOf checks. goNum is the field's
// Go numeric type ("int64" or "float64"); checks are skipped for other types so
// the emitted literals always match the operand's type.
func (e *emitter) numericChecks(out *[]jen.Code, s *ir.Schema, goNum string, val func() *jen.Statement, errf func(string) jen.Code) {
	if goNum != "int64" && goNum != "float64" {
		return
	}
	cmp := func(bound *big.Rat, op, msg string) {
		if bound == nil {
			return
		}
		*out = append(*out, jen.If(val().Op(op).Add(numLit(bound, goNum))).Block(errf(msg)))
	}
	cmp(s.Minimum, "<", "below minimum")
	cmp(s.Maximum, ">", "above maximum")
	cmp(s.ExclusiveMinimum, "<=", "at or below exclusive minimum")
	cmp(s.ExclusiveMaximum, ">=", "at or above exclusive maximum")
	if s.MultipleOf != nil {
		if goNum == "int64" && s.MultipleOf.IsInt() {
			*out = append(*out, jen.If(val().Op("%").Add(numLit(s.MultipleOf, goNum)).Op("!=").Lit(0)).Block(errf("not a multiple")))
		} else {
			f, _ := s.MultipleOf.Float64()
			*out = append(*out, jen.If(jen.Qual("math", "Mod").Call(jen.Add(val()), jen.Lit(f)).Op("!=").Lit(0.0)).Block(errf("not a multiple")))
		}
	}
}

// fieldNumKind returns the field's Go numeric type ("int64"/"float64"), or "".
func fieldNumKind(fld *gotype.Field) string {
	if fld.Type.Named == "" && fld.Type.Slice == nil && fld.Type.Map == nil {
		return fld.Type.Prim
	}
	return ""
}

func (e *emitter) emitEnum(f *jen.File, d *gotype.Decl) {
	e.emitTypeDoc(f, d, "")
	f.Type().Id(d.Name).Add(e.typeCode(d.Underlying))
	defs := make([]jen.Code, 0, len(d.Enum))
	cases := make([]jen.Code, 0, len(d.Enum))
	for _, ev := range d.Enum {
		defs = append(defs, jen.Id(ev.Ident).Id(d.Name).Op("=").Add(litValue(d.Underlying, ev.Value)))
		cases = append(cases, jen.Id(ev.Ident))
	}
	f.Comment("The permitted " + d.Name + " values.")
	f.Const().Defs(defs...)
	f.Comment("Validate reports whether x is one of the permitted " + d.Name + " values.")
	f.Func().Params(jen.Id("x").Id(d.Name)).Id("Validate").Params().Error().Block(
		jen.Switch(jen.Id("x")).Block(
			jen.Case(cases...).Block(jen.Return(jen.Nil())),
		),
		jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit(d.Name+": invalid value %v"), jen.Id("x"))),
	)
}

func (e *emitter) emitAlias(f *jen.File, d *gotype.Decl) {
	e.emitTypeDoc(f, d, "")
	f.Type().Id(d.Name).Add(e.typeCode(d.Underlying))
}

// emitInterface emits a oneOf/anyOf as a sealed marker interface, a marker
// method on each variant, and an Unmarshal<Name> dispatcher.
func (e *emitter) emitInterface(f *jen.File, d *gotype.Decl) {
	marker := "is" + d.Name
	e.emitTypeDoc(f, d, " It is a closed union implemented by its variant types.")
	f.Type().Id(d.Name).Interface(jen.Id(marker).Params())

	for _, v := range d.Variants {
		f.Func().Params(jen.Id("_").Id(v.Named)).Id(marker).Params().Block()
	}

	e.emitUnmarshalInterface(f, d)
}

// emitUnmarshalInterface generates Unmarshal<Name>, which trial-decodes each
// variant (rejecting ones that fail their own UnmarshalJSON or Validate) and
// enforces oneOf's exactly-one rule.
func (e *emitter) emitUnmarshalInterface(f *jen.File, d *gotype.Decl) {
	var attempts []jen.Code
	for i, v := range d.Variants {
		vv := fmt.Sprintf("v%d", i)
		attempts = append(attempts,
			jen.Block(
				jen.Var().Id(vv).Id(v.Named),
				jen.If(jen.Err().Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id(vv)), jen.Err().Op("==").Nil()).Block(
					jen.If(jen.List(jen.Id("val"), jen.Id("ok")).Op(":=").Any().Call(jen.Op("&").Id(vv)).Assert(jen.Interface(jen.Id("Validate").Params().Error())), jen.Op("!").Id("ok").Op("||").Id("val").Dot("Validate").Call().Op("==").Nil()).Block(
						jen.Id("matches").Op("=").Append(jen.Id("matches"), jen.Op("&").Id(vv)),
					),
				),
			),
		)
	}

	body := []jen.Code{jen.Var().Id("matches").Index().Id(d.Name)}
	body = append(body, attempts...)

	var tail jen.Code
	if d.Exclusive {
		tail = jen.Switch(jen.Len(jen.Id("matches"))).Block(
			jen.Case(jen.Lit(1)).Block(jen.Return(jen.Id("matches").Index(jen.Lit(0)), jen.Nil())),
			jen.Case(jen.Lit(0)).Block(jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(d.Name+": value matches no variant")))),
			jen.Default().Block(jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(d.Name+": value matches %d variants, want exactly one"), jen.Len(jen.Id("matches"))))),
		)
	} else {
		tail = jen.If(jen.Len(jen.Id("matches")).Op(">").Lit(0)).Block(
			jen.Return(jen.Id("matches").Index(jen.Lit(0)), jen.Nil()),
		).Line().Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(d.Name+": value matches no variant")))
	}
	body = append(body, tail)

	f.Comment("Unmarshal" + d.Name + " decodes data into whichever " + d.Name + " variant matches.")
	f.Func().Id("Unmarshal"+d.Name).Params(jen.Id("data").Index().Byte()).Params(jen.Id(d.Name), jen.Error()).Block(body...)
}

func (e *emitter) patternVarFor(pattern string) string {
	for _, p := range e.patterns {
		if p.pattern == pattern {
			return p.name
		}
	}
	name := fmt.Sprintf("pattern%d", len(e.patterns))
	e.patterns = append(e.patterns, patternVar{name: name, pattern: pattern})
	return name
}

func (e *emitter) typeCode(t *gotype.TypeRef) *jen.Statement {
	switch {
	case t.Slice != nil:
		return jen.Index().Add(e.typeCode(t.Slice))
	case t.Map != nil:
		return jen.Map(jen.String()).Add(e.typeCode(t.Map))
	}
	var base *jen.Statement
	switch {
	case t.Named != "":
		base = jen.Id(t.Named)
	case t.Import != "":
		base = jen.Qual(t.Import, t.Prim)
	default:
		base = jen.Id(t.Prim)
	}
	if t.Pointer {
		return jen.Op("*").Add(base)
	}
	return base
}

// accessorForValidate yields the receiver expression for a nested Validate call.
func accessorForValidate(fld *gotype.Field) *jen.Statement {
	return jen.Id("x").Dot(fld.Name)
}

// numLit renders a rational as a Go literal in the given numeric type, so it
// matches the operand it is compared against.
func numLit(r *big.Rat, goNum string) *jen.Statement {
	if goNum == "int64" && r.IsInt() {
		return jen.Lit(r.Num().Int64())
	}
	f, _ := r.Float64()
	return jen.Lit(f)
}

func litValue(underlying *gotype.TypeRef, v any) *jen.Statement {
	switch underlying.Prim {
	case "string":
		return jen.Lit(fmt.Sprint(v))
	case "int64":
		if n, ok := v.(interface{ Int64() (int64, error) }); ok {
			if i, err := n.Int64(); err == nil {
				return jen.Lit(i)
			}
		}
		return jen.Lit(fmt.Sprint(v))
	case "float64":
		if n, ok := v.(interface{ Float64() (float64, error) }); ok {
			if fl, err := n.Float64(); err == nil {
				return jen.Lit(fl)
			}
		}
		return jen.Lit(fmt.Sprint(v))
	default:
		return jen.Lit(fmt.Sprint(v))
	}
}
