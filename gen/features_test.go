package gen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/jsonschema/gotype"
	"github.com/ChristopherDavenport/jsonschema/ir"
	"github.com/ChristopherDavenport/jsonschema/loader"
)

// --- Runtime behavior (generated code is compiled and run) ---------------------

const nullableSchema = `{
  "type": "object",
  "properties": {
    "nick":  {"type": ["string", "null"]},
    "score": {"type": ["number", "null"]}
  }
}`

const nullableTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestNullableScalar(t *testing.T) {
	// A nullable scalar maps to its underlying Go scalar (*string, *float64), not
	// any; dereferencing as a typed value must compile and hold.
	var x Rec
	if err := json.Unmarshal([]byte(` + "`" + `{"nick":"al","score":1.5}` + "`" + `), &x); err != nil {
		t.Fatalf("unmarshal value: %v", err)
	}
	if x.Nick == nil || *x.Nick != "al" {
		t.Fatalf("nick is not a *string: %#v", x.Nick)
	}
	if x.Score == nil || *x.Score != 1.5 {
		t.Fatalf("score is not a *float64: %#v", x.Score)
	}
	// An explicit null decodes to a nil pointer.
	var y Rec
	if err := json.Unmarshal([]byte(` + "`" + `{"nick":null}` + "`" + `), &y); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if y.Nick != nil {
		t.Fatalf("null should decode to a nil pointer, got %#v", y.Nick)
	}
}
`

// TestGeneratedNullableScalar proves a ["string","null"] type decodes as its
// underlying Go scalar rather than any.
func TestGeneratedNullableScalar(t *testing.T) {
	runInModule(t, nullableSchema, Config{Package: "gentest", RootName: "Rec"}, nullableTest)
}

const patternPropsSchema = `{
  "type": "object",
  "properties": {
    "counts": {"type": "object", "patternProperties": {"^x-": {"type": "integer"}}}
  }
}`

const patternPropsTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestPatternPropertiesTyped(t *testing.T) {
	var d Doc
	if err := json.Unmarshal([]byte(` + "`" + `{"counts":{"x-a":3,"x-b":7}}` + "`" + `), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The map value type comes from the patternProperties schema (int64), not any:
	// this assignment only compiles if the element type is int64.
	var got int64 = d.Counts["x-a"]
	if got != 3 || d.Counts["x-b"] != 7 {
		t.Fatalf("typed patternProperties map wrong: %#v", d.Counts)
	}
}
`

// TestGeneratedPatternProperties proves a patternProperties value schema types
// the generated map (previously only additionalProperties did).
func TestGeneratedPatternProperties(t *testing.T) {
	runInModule(t, patternPropsSchema, Config{Package: "gentest", RootName: "Doc"}, patternPropsTest)
}

const intEnumSchema = `{
  "type": "object",
  "properties": {"grade": {"$ref": "#/$defs/Grade"}},
  "$defs": {"Grade": {"type": "integer", "enum": [1, 2, 3]}}
}`

const intEnumTest = `package gentest

import "testing"

func TestIntegerEnum(t *testing.T) {
	// The constants are emitted as untyped literals, so they are assignable to the
	// defined enum type; a typed int64(1) would not compile against Grade here.
	if GradeX1 != 1 || GradeX2 != 2 || GradeX3 != 3 {
		t.Fatalf("enum constants wrong: %d %d %d", GradeX1, GradeX2, GradeX3)
	}
	if err := GradeX2.Validate(); err != nil {
		t.Fatalf("member value rejected: %v", err)
	}
	if Grade(9).Validate() == nil {
		t.Fatal("a non-member value should fail Validate")
	}
}
`

// TestGeneratedIntegerEnum proves integer enum constants are emitted as untyped
// literals — the point of the fix is that the generated code compiles.
func TestGeneratedIntegerEnum(t *testing.T) {
	runInModule(t, intEnumSchema, Config{Package: "gentest", RootName: "Root"}, intEnumTest)
}

// --- Generated shape (assert on emitted source) -------------------------------

// TestScalarRefKeepsNamedType is the regression guard for finding #2: a $ref to a
// plain-scalar $def must reference the named type, not inline the bare scalar and
// leave the declaration orphaned. It also pins that a $ref co-occurring with a
// sibling keyword still follows the ref.
func TestScalarRefKeepsNamedType(t *testing.T) {
	const schema = `{
	  "type": "object",
	  "properties": {
	    "currency": {"$ref": "#/$defs/Code"},
	    "note":     {"$ref": "#/$defs/Plain", "description": "ref with a sibling keyword"}
	  },
	  "$defs": {
	    "Code":  {"type": "string", "pattern": "^[A-Z]{3}$"},
	    "Plain": {"type": "string"}
	  }
	}`
	out, err := Generate(Config{Package: "p", RootName: "Money"}, []byte(schema))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	src := string(out)
	for _, want := range []string{"Currency *Code", "Note *Plain", "type Code string", "type Plain string"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
	if strings.Contains(src, "Currency *string") || strings.Contains(src, "map[string]any") {
		t.Errorf("scalar ref was inlined or degraded:\n%s", src)
	}
}

// TestHeaderCommentOverride proves Config.HeaderComment replaces the default
// generated-file banner.
func TestHeaderCommentOverride(t *testing.T) {
	const schema = `{"type":"object","properties":{"a":{"type":"string"}}}`
	out, err := Generate(Config{Package: "p", RootName: "Root", HeaderComment: "Custom banner - hand tuned"}, []byte(schema))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	src := string(out)
	if !strings.Contains(src, "// Custom banner - hand tuned") {
		t.Errorf("custom header not emitted:\n%s", src)
	}
	if strings.Contains(src, "DO NOT EDIT") {
		t.Errorf("default banner should have been replaced:\n%s", src)
	}
}

// TestOmitRequiredChecks proves Config.OmitRequiredChecks drops the
// required-property presence check from the generated UnmarshalJSON.
func TestOmitRequiredChecks(t *testing.T) {
	const schema = `{"type":"object","required":["id"],"properties":{"id":{"type":"string"},"name":{"type":"string"}}}`
	strict, err := Generate(Config{Package: "p", RootName: "Root"}, []byte(schema))
	if err != nil {
		t.Fatalf("generate default: %v", err)
	}
	if !strings.Contains(string(strict), "missing required property") {
		t.Fatalf("default output should carry the required-presence check:\n%s", strict)
	}
	lenient, err := Generate(Config{Package: "p", RootName: "Root", OmitRequiredChecks: true}, []byte(schema))
	if err != nil {
		t.Fatalf("generate lenient: %v", err)
	}
	if strings.Contains(string(lenient), "missing required property") {
		t.Errorf("OmitRequiredChecks should drop the required-presence check:\n%s", lenient)
	}
}

// --- Multi-root / source-order / external-ref (gotype + EmitModel) ------------

// emitModelSource runs the driver-facing pipeline: parse, load, analyze (via
// Analyze or a caller-supplied set of roots), and emit — returning the source so
// a test can assert on the shape the gotype Config produced.
func emitModelSource(t *testing.T, doc string, cfg gotype.Config, rootsFn func(*ir.Schema) []gotype.NamedRoot) string {
	t.Helper()
	var s ir.Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("parse: %v", err)
	}
	ldr := loader.New()
	ldr.AddSchema("", &s)

	var (
		model *gotype.Model
		err   error
	)
	if rootsFn != nil {
		model, err = gotype.AnalyzeRoots(rootsFn(&s), ldr, cfg)
	} else {
		model, err = gotype.Analyze(&s, ldr, cfg)
	}
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, _, err := EmitModel(Config{Package: cfg.Package}, model, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return string(out)
}

// TestSourceOrderFields proves Config.SourceOrder emits struct fields in the
// order their properties appeared in the document, and that the default remains
// alphabetical.
func TestSourceOrderFields(t *testing.T) {
	const doc = `{"type":"object","properties":{"zebra":{"type":"string"},"apple":{"type":"string"},"mango":{"type":"string"}}}`

	sorted := emitModelSource(t, doc, gotype.Config{Package: "p", RootName: "Root"}, nil)
	if !(strings.Index(sorted, "Apple") < strings.Index(sorted, "Mango") &&
		strings.Index(sorted, "Mango") < strings.Index(sorted, "Zebra")) {
		t.Errorf("default field order should be alphabetical:\n%s", sorted)
	}

	src := emitModelSource(t, doc, gotype.Config{Package: "p", RootName: "Root", SourceOrder: true}, nil)
	if !(strings.Index(src, "Zebra") < strings.Index(src, "Apple") &&
		strings.Index(src, "Apple") < strings.Index(src, "Mango")) {
		t.Errorf("SourceOrder should keep source field order:\n%s", src)
	}
}

// TestAnalyzeRootsMultiRoot proves several named roots analyze into one package
// with a top-level declaration each.
func TestAnalyzeRootsMultiRoot(t *testing.T) {
	const doc = `{
	  "$defs": {
	    "alpha": {"type": "object", "properties": {"a": {"type": "string"}}},
	    "beta":  {"type": "object", "properties": {"b": {"type": "integer"}}}
	  }
	}`
	src := emitModelSource(t, doc, gotype.Config{Package: "multi"}, func(s *ir.Schema) []gotype.NamedRoot {
		return []gotype.NamedRoot{
			{Name: "Alpha", Schema: s.Defs["alpha"]},
			{Name: "Beta", Schema: s.Defs["beta"]},
		}
	})
	if !strings.Contains(src, "package multi") {
		t.Errorf("wrong package:\n%s", src)
	}
	for _, want := range []string{"type Alpha struct", "type Beta struct"} {
		if !strings.Contains(src, want) {
			t.Errorf("multi-root model missing %q:\n%s", want, src)
		}
	}
}

// TestExternalRef proves Config.ExternalRef routes a referenced type to another
// package (a qualified reference, its import, and no local declaration).
func TestExternalRef(t *testing.T) {
	const doc = `{
	  "type": "object",
	  "properties": {"item": {"$ref": "#/$defs/Shared"}},
	  "$defs": {"Shared": {"type": "object", "properties": {"id": {"type": "string"}}}}
	}`
	cfg := gotype.Config{
		Package:  "p",
		RootName: "Root",
		ExternalRef: func(ref string, _ *ir.Schema) (pkg, goName, importPath string, ok bool) {
			if strings.HasSuffix(ref, "/Shared") {
				return "shared", "Shared", "example.com/shared", true
			}
			return "", "", "", false
		},
	}
	// Only the root is declared, so Shared is reached solely through the $ref and
	// must be routed externally rather than generated in this model.
	src := emitModelSource(t, doc, cfg, func(s *ir.Schema) []gotype.NamedRoot {
		return []gotype.NamedRoot{{Name: "Root", Schema: s}}
	})
	if !strings.Contains(src, "shared.Shared") {
		t.Errorf("external ref should be package-qualified:\n%s", src)
	}
	if !strings.Contains(src, `"example.com/shared"`) {
		t.Errorf("external import should be added:\n%s", src)
	}
	if strings.Contains(src, "type Shared struct") {
		t.Errorf("external type should not be declared locally:\n%s", src)
	}
}
