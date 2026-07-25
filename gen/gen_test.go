package gen

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

const personSchema = `{
  "$id": "https://example.com/person.json",
  "type": "object",
  "required": ["id", "name"],
  "properties": {
    "id":    {"type": "string", "format": "uuid"},
    "name":  {"type": "string", "minLength": 1, "maxLength": 100},
    "age":   {"type": "integer", "minimum": 0, "maximum": 150},
    "email": {"type": "string", "format": "email"},
    "role":  {"enum": ["admin", "user", "guest"]},
    "tags":  {"type": "array", "items": {"type": "string"}},
    "address": {"$ref": "#/$defs/address"}
  },
  "$defs": {
    "address": {
      "type": "object",
      "required": ["street"],
      "properties": {
        "street": {"type": "string"},
        "zip":    {"type": "string", "pattern": "^[0-9]{5}$"}
      }
    }
  }
}`

func TestGenerateGolden(t *testing.T) {
	out, err := Generate(Config{Package: "person", RootName: "Person", AssertFormat: true}, []byte(personSchema))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	golden := filepath.Join("testdata", "person.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if string(out) != string(want) {
		t.Errorf("generated output differs from golden; run: go test ./gen -update")
	}
}

// TestGeneratedCompiles writes generated code into a throwaway module that
// replaces this module locally, then `go test`s it — proving the output
// compiles, type-checks, and enforces the schema at runtime.
func TestGeneratedCompiles(t *testing.T) {
	runInModule(t, personSchema, Config{Package: "gentest", RootName: "Person", AssertFormat: true}, semanticTest)
}

// TestGeneratedOneOf proves the oneOf interface, dispatcher, and variant
// selection work end to end.
func TestGeneratedOneOf(t *testing.T) {
	runInModule(t, oneOfSchema, Config{Package: "gentest", RootName: "Order"}, oneOfTest)
}

// TestGeneratedXGoMap proves the x-go type override and additionalProperties
// maps generate correct, working code.
func TestGeneratedXGoMap(t *testing.T) {
	runInModule(t, xgoSchema, Config{Package: "gentest", RootName: "Doc"}, xgoTest)
}

// TestGeneratedTuple proves prefixItems tuples round-trip as JSON arrays.
func TestGeneratedTuple(t *testing.T) {
	runInModule(t, tupleSchema, Config{Package: "gentest", RootName: "Point"}, tupleTest)
}

// TestGeneratedNumeric guards against emitting mistyped numeric literals
// (e.g. an int64 bound compared against a float64 field).
func TestGeneratedNumeric(t *testing.T) {
	runInModule(t, numSchema, Config{Package: "gentest", RootName: "Numbers"}, numTest)
}

// TestGeneratedDiscriminator proves a string-discriminator if/then is enforced
// inline (no engine fallback), the idiomatic path.
func TestGeneratedDiscriminator(t *testing.T) {
	runInModule(t, condSchema, Config{Package: "gentest", RootName: "Item"}, condTest)
}

// TestGeneratedEnumDiscriminator proves the same if/then is enforced inline when
// the tag is an enum, comparing against the generated constant. Writing the tag
// as an `enum` is the most natural way to spell a tagged union; requiring the Go
// type to be exactly `string` made it the most common surprise in the docs.
func TestGeneratedEnumDiscriminator(t *testing.T) {
	runInModule(t, enumCondSchema, Config{Package: "gentest", RootName: "Item"}, enumCondTest)
}

// TestGeneratedEngineFallback proves that with EngineFallback, a keyword that is
// never mirrored inline (dependentSchemas) is enforced via the runtime engine.
func TestGeneratedEngineFallback(t *testing.T) {
	runInModule(t, fallbackSchema, Config{Package: "gentest", RootName: "Item", EngineFallback: true}, fallbackTest)
}

// TestGeneratedRemoteRef proves an unbundled remote $ref can be supplied at
// runtime through the generated AddSchemaResource hook, rather than forcing the
// caller to bundle every remote document into the input before generating.
func TestGeneratedRemoteRef(t *testing.T) {
	runInModule(t, remoteRefSchema, Config{Package: "gentest", RootName: "Item", EngineFallback: true}, remoteRefTest)
}

// TestGeneratedAllOf proves allOf is modeled as struct embedding with each
// part's required fields and Validate enforced.
func TestGeneratedAllOf(t *testing.T) {
	runInModule(t, allOfSchema, Config{Package: "gentest", RootName: "Record"}, allOfTest)
}

// TestGeneratedAllOfMerge proves the composed type marshals as one flat object:
// a property two parts declare is emitted once rather than dropped as an
// ambiguous promoted field, and a dictionary part contributes its entries
// instead of nesting under its type name. Both round-trip.
func TestGeneratedAllOfMerge(t *testing.T) {
	runInModule(t, mergeSchema, Config{Package: "gentest", RootName: "Record"}, mergeTest)
}

// TestGeneratedEmptyCollectionsSurvive proves an empty-but-present array or
// object round-trips. Under `omitempty` an optional empty slice marshaled away
// entirely, so a document that satisfied the schema on the way in failed its own
// `required` check on the way out — a false reject no NOTE could warn about,
// because it depends on the value rather than the schema. `omitzero` omits only
// the zero value, so nil is dropped and empty-non-nil survives.
func TestGeneratedEmptyCollectionsSurvive(t *testing.T) {
	runInModule(t, emptyCollectionSchema, Config{Package: "gentest", RootName: "Item"}, emptyCollectionTest)
}

// tags is optional at the top level — so it carries the omit tag — but becomes
// required once kind is present. That is where erasing an empty-but-present
// value changes the answer: the document is valid going in and invalid coming
// back out.
const emptyCollectionSchema = `{
  "type": "object",
  "properties": {
    "kind": {"type": "string"},
    "tags": {"type": "array", "items": {"type": "string"}},
    "note": {"type": "string"}
  },
  "dependentRequired": {"kind": ["tags"]}
}`

const emptyCollectionTest = `package gentest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmptyCollectionsSurvive(t *testing.T) {
	const doc = ` + "`" + `{"kind":"k","tags":[]}` + "`" + `
	var x Item
	if err := json.Unmarshal([]byte(doc), &x); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if x.Tags == nil || len(x.Tags) != 0 {
		t.Fatalf("empty array should decode to an empty non-nil slice: %#v", x.Tags)
	}
	if err := x.Validate(); err != nil {
		t.Fatalf("the document is valid as given: %v", err)
	}

	b, err := json.Marshal(x)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), ` + "`" + `"tags":[]` + "`" + `) {
		t.Errorf("empty-but-present array did not survive marshal: %s", b)
	}
	// A nil optional field is still omitted — omitzero must not turn into "always".
	if strings.Contains(string(b), "note") {
		t.Errorf("nil optional field should be omitted: %s", b)
	}

	// The type reads back its own output and still satisfies the schema. This is
	// the false reject: under omitempty "tags" was erased here, and the
	// dependentRequired check then failed on a document that was always valid.
	var back Item
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("re-unmarshal own output %s: %v", b, err)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("valid document rejected after round trip (%s): %v", b, err)
	}
}
`

// TestConservativeShapes pins the shapes that look like an inline path but are
// deliberately refused, so they surface as a NOTE instead of a Validate that
// quietly ignores part of the schema.
func TestConservativeShapes(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		absent  string // generated source must not contain this
		note    string // the NOTE must name this exact condition
		comment string
	}{{
		name:    "then requires an undeclared property",
		schema:  `{"type":"object","properties":{"kind":{"type":"string"}},"if":{"properties":{"kind":{"const":"secret"}}},"then":{"required":["value"]}}`,
		absent:  `*x.Kind == "secret"`,
		note:    `then requires "value", which is not a declared property`,
		comment: "no field backs `value`, so the requirement is uncheckable inline",
	}, {
		name:    "tag property carries an extra assertion",
		schema:  `{"type":"object","properties":{"kind":{"type":"string"},"value":{"type":"string"}},"if":{"properties":{"kind":{"const":"secret","minLength":99}}},"then":{"required":["value"]}}`,
		absent:  `*x.Kind == "secret"`,
		note:    `if property "kind" is not a plain string const/enum match`,
		comment: "minLength inside the if would be dropped by a plain tag comparison",
	}, {
		name:    "allOf member is not an object schema",
		schema:  `{"allOf":[{"type":"string","minLength":2},{"$ref":"#/$defs/a"}],"$defs":{"a":{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}}}`,
		absent:  "\tA\n",
		note:    "allOf: member 1 is not an object schema",
		comment: "a scalar member has no properties to merge into the parent object",
	}, {
		// The if/then here IS mirrored; only `not` is not. The NOTE must say so
		// rather than implicating the whole family.
		name:    "only the unenforced keyword is named",
		schema:  `{"type":"object","properties":{"kind":{"type":"string"},"value":{"type":"string"}},"if":{"properties":{"kind":{"const":"secret"}}},"then":{"required":["value"]},"not":{"required":["ghost"]}}`,
		absent:  "if/then/else",
		note:    "  - not\n",
		comment: "the discriminator if is enforced; only `not` is not",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Generate(Config{Package: "p", RootName: "Root"}, []byte(tc.schema))
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			src := string(out)
			if strings.Contains(src, tc.absent) {
				t.Errorf("generated code contains %q (%s):\n%s", tc.absent, tc.comment, src)
			}
			if !strings.Contains(src, tc.note) {
				t.Errorf("NOTE does not name the condition %q (%s):\n%s", tc.note, tc.comment, src)
			}
		})
	}
}

// TestNoteSaysWhetherFallbackHelps pins that the NOTE distinguishes keywords
// -engine-fallback would enforce from those it cannot, because they constrain a
// property the Go type does not declare and so cannot survive the marshal round
// trip the delegating Validate performs.
func TestNoteSaysWhetherFallbackHelps(t *testing.T) {
	const overDeclared = `{"type":"object","properties":{"a":{"type":"string"}},"not":{"required":["a"]}}`
	const overUndeclared = `{"type":"object","properties":{"a":{"type":"string"}},"not":{"required":["ghost"]}}`
	const mixed = `{"type":"object","properties":{"kind":{"type":"string"},"value":{"type":"string"}},` +
		`"dependentSchemas":{"kind":{"required":["value"]}},"not":{"required":["ghost"]}}`

	cases := []struct {
		name     string
		schema   string
		fallback bool
		want     []string
		notWant  []string
	}{{
		name:    "fallback resolves it",
		schema:  overDeclared,
		want:    []string{"Regenerate with -engine-fallback"},
		notWant: []string{"cannot enforce"},
	}, {
		name:    "fallback cannot resolve it",
		schema:  overUndeclared,
		want:    []string{`cannot enforce "not"`, `"ghost"`, "original document"},
		notWant: []string{"Regenerate with -engine-fallback, or validate"},
	}, {
		name:   "one of each",
		schema: mixed,
		want: []string{
			`-engine-fallback to enforce "dependentSchemas"`,
			`It cannot enforce "not"`,
		},
	}, {
		// Even with the flag on, the delegating Validate must admit what the
		// round trip loses rather than claim full conformance.
		name:     "delegating Validate admits the gap",
		schema:   overUndeclared,
		fallback: true,
		want:     []string{"delegating to the", `NOTE: "not" is not enforced faithfully even here`, `"ghost"`},
	}, {
		name:     "delegating Validate is clean when faithful",
		schema:   overDeclared,
		fallback: true,
		want:     []string{"delegating to the"},
		notWant:  []string{"NOTE:"},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Generate(Config{Package: "p", RootName: "Root", EngineFallback: tc.fallback}, []byte(tc.schema))
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			// Comment prose is wrapped, so assert against the reflowed text.
			flowed := flowComments(string(out))
			for _, want := range tc.want {
				if !strings.Contains(flowed, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
			for _, no := range tc.notWant {
				if strings.Contains(flowed, no) {
					t.Errorf("unexpected %q in:\n%s", no, out)
				}
			}
		})
	}
}

// TestReport pins the machine-readable report the CLI prints: it lists exactly
// what the output does not enforce, and drops an item once the generated code
// actually enforces it.
func TestReport(t *testing.T) {
	const schema = `{"type":"object",
	  "properties":{"kind":{"type":"string"},"value":{"type":"string"}},
	  "dependentSchemas":{"kind":{"required":["value"]}},
	  "not":{"required":["ghost"]}}`

	_, report, err := GenerateWithReport(Config{Package: "p", RootName: "Item"}, []byte(schema))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if report.Empty() || len(report.Types) != 1 {
		t.Fatalf("want one reported type, got %+v", report)
	}
	got := report.Types[0]
	if got.Type != "Item" || !got.HasValidate || got.Delegates {
		t.Errorf("type report wrong: %+v", got)
	}
	if len(got.Items) != 2 {
		t.Fatalf("want both keywords reported, got %+v", got.Items)
	}
	byKeyword := map[string]Unenforced{}
	for _, it := range got.Items {
		byKeyword[it.Keyword] = it
	}
	if it := byKeyword["dependentSchemas"]; !it.FallbackWouldEnforce {
		t.Errorf("dependentSchemas over declared properties is fixable by the flag: %+v", it)
	}
	if it := byKeyword["not"]; it.FallbackWouldEnforce || len(it.Properties) != 1 || it.Properties[0] != "ghost" {
		t.Errorf(`not over an undeclared "ghost" is not fixable by the flag: %+v`, it)
	}
	if n := report.FallbackWouldFix(); n != 1 {
		t.Errorf("FallbackWouldFix() = %d, want 1", n)
	}
	if s := report.String(); !strings.Contains(s, "Item") || !strings.Contains(s, "would enforce 1 constraint") {
		t.Errorf("rendered report reads wrong:\n%s", s)
	}

	// With the flag on, the engine enforces dependentSchemas, so only the item
	// the marshal round trip cannot carry is still reported.
	_, report, err = GenerateWithReport(Config{Package: "p", RootName: "Item", EngineFallback: true}, []byte(schema))
	if err != nil {
		t.Fatalf("generate with fallback: %v", err)
	}
	if len(report.Types) != 1 || len(report.Types[0].Items) != 1 ||
		report.Types[0].Items[0].Keyword != "not" || !report.Types[0].Delegates {
		t.Errorf("with -engine-fallback, want only `not` reported on a delegating type: %+v", report.Types)
	}
	if n := report.FallbackWouldFix(); n != 0 {
		t.Errorf("nothing left for the flag to fix, got %d", n)
	}

	// A schema the generator mirrors completely reports nothing.
	_, report, err = GenerateWithReport(Config{Package: "p", RootName: "Clean"},
		[]byte(`{"type":"object","required":["a"],"properties":{"a":{"type":"string","minLength":2}}}`))
	if err != nil {
		t.Fatalf("generate clean: %v", err)
	}
	if !report.Empty() || report.String() != "" {
		t.Errorf("fully-enforced schema should report nothing, got %+v", report)
	}
}

// TestUnmirroredKeywordsAreAnnounced pins the invariant the whole design rests
// on: every constraint the generated code does not enforce reaches the NOTE and
// the report, so -strict fails on it. Before this, a schema whose only constraint
// was `minProperties: 2` generated a Validate returning nil, reported nothing,
// and passed -strict clean — a silent false accept, the one gap that could burn
// someone who read the docs and did everything right.
func TestUnmirroredKeywordsAreAnnounced(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		detail   string // the NOTE and report must name this
		fallback bool   // whether -engine-fallback would enforce it faithfully
	}{{
		name:   "minProperties",
		schema: `{"type":"object","minProperties":2,"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`,
		detail: "minProperties (2)",
		// A struct marshals only its declared properties, so the engine would
		// count a different property set than the input had.
		fallback: false,
	}, {
		name:     "maxProperties",
		schema:   `{"type":"object","maxProperties":1,"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`,
		detail:   "maxProperties (1)",
		fallback: false,
	}, {
		name:     "patternProperties",
		schema:   `{"type":"object","patternProperties":{"^x-":{"type":"string"}},"properties":{"a":{"type":"string"}}}`,
		detail:   `patternProperties ("^x-")`,
		fallback: false,
	}, {
		name:     "propertyNames",
		schema:   `{"type":"object","propertyNames":{"maxLength":3},"properties":{"a":{"type":"string"}}}`,
		detail:   "propertyNames",
		fallback: false,
	}, {
		name:     "unevaluatedProperties",
		schema:   `{"type":"object","unevaluatedProperties":false,"properties":{"a":{"type":"string"}}}`,
		detail:   "unevaluatedProperties",
		fallback: false,
	}, {
		name:   "contains on a property",
		schema: `{"type":"object","properties":{"t":{"type":"array","items":{"type":"string"},"contains":{"const":"x"}}}}`,
		detail: `contains on property "t"`,
		// A slice marshals intact, so delegation sees the same array.
		fallback: true,
	}, {
		name:     "minContains on a property",
		schema:   `{"type":"object","properties":{"t":{"type":"array","items":{"type":"string"},"minContains":2,"contains":{"const":"x"}}}}`,
		detail:   `minContains (2) on property "t"`,
		fallback: true,
	}, {
		// The four announced families were only announced on a *type*. On a
		// property they were dropped as silently as minProperties.
		name:     "not on a property",
		schema:   `{"type":"object","properties":{"t":{"type":"string","not":{"const":"x"}}}}`,
		detail:   `not on property "t"`,
		fallback: true,
	}, {
		name:     "if/then/else on a property",
		schema:   `{"type":"object","properties":{"t":{"type":"string","if":{"const":"a"},"then":{"maxLength":1}}}}`,
		detail:   `if/then/else on property "t"`,
		fallback: true,
	}, {
		// fieldChecks skips a pattern RE2 cannot compile, and numeric bounds on a
		// field that is not a Go numeric. Both were silent drops.
		name:     "pattern RE2 cannot compile",
		schema:   `{"type":"object","properties":{"t":{"type":"string","pattern":"(?<=a)b"}}}`,
		detail:   `pattern (not expressible in RE2) on property "t"`,
		fallback: true,
	}, {
		name:     "numeric bound on a non-numeric field",
		schema:   `{"type":"object","properties":{"t":{"minimum":3}}}`,
		detail:   `minimum (the Go type is not a numeric) on property "t"`,
		fallback: true,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, report, err := GenerateWithReport(Config{Package: "p", RootName: "Root"}, []byte(tc.schema))
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			// The report is what -strict gates on: it must not be empty.
			if report.Empty() {
				t.Fatalf("-strict would pass clean on an unenforced %s:\n%s", tc.name, out)
			}
			if !strings.Contains(report.String(), tc.detail) {
				t.Errorf("report does not name %q:\n%s", tc.detail, report.String())
			}
			// The generated source must say the same thing.
			if !strings.Contains(flowComments(string(out)), tc.detail) {
				t.Errorf("NOTE does not name %q:\n%s", tc.detail, out)
			}
			// And it must not still claim full conformance.
			if strings.Contains(string(out), "Validate reports whether x satisfies the schema.") {
				t.Errorf("Validate still claims to satisfy the whole schema:\n%s", out)
			}

			var found *Unenforced
			for i, ty := range report.Types {
				for j, it := range ty.Items {
					if strings.Contains(it.Detail, tc.detail) {
						found = &report.Types[i].Items[j]
					}
				}
			}
			if found == nil {
				t.Fatalf("no report item with detail %q: %+v", tc.detail, report.Types)
			}
			if found.FallbackWouldEnforce != tc.fallback {
				t.Errorf("FallbackWouldEnforce = %v, want %v for %s (Why: %s)",
					found.FallbackWouldEnforce, tc.fallback, tc.name, found.Why)
			}
			if !tc.fallback && found.Why == "" {
				t.Errorf("an item the flag cannot fix must say why: %+v", found)
			}
		})
	}
}

// TestAnnotationKeywordsAreNotReported pins the other half of the invariant: the
// report names real gaps only. contentEncoding, contentMediaType and
// contentSchema are annotations in the standard vocabularies — the runtime engine
// does not assert them either — so reporting them would send readers chasing a
// constraint that does not exist, and would falsely promise -engine-fallback
// fixes it.
func TestAnnotationKeywordsAreNotReported(t *testing.T) {
	for _, schema := range []string{
		`{"type":"object","properties":{"t":{"type":"string","contentEncoding":"base64"}}}`,
		`{"type":"object","properties":{"t":{"type":"string","contentMediaType":"application/json"}}}`,
		`{"type":"object","properties":{"t":{"type":"string","contentSchema":{"type":"object"}}}}`,
		// Keywords that assert nothing are not gaps either.
		`{"type":"object","minProperties":0,"properties":{"a":{"type":"string"}}}`,
		`{"type":"object","unevaluatedProperties":true,"properties":{"a":{"type":"string"}}}`,
	} {
		_, report, err := GenerateWithReport(Config{Package: "p", RootName: "Root"}, []byte(schema))
		if err != nil {
			t.Fatalf("generate %s: %v", schema, err)
		}
		if !report.Empty() {
			t.Errorf("nothing is unenforced in %s, but it reported:\n%s", schema, report.String())
		}
	}
}

// flowComments strips comment markers and collapses whitespace, so a test can
// match a sentence without caring where the generator wrapped it.
func flowComments(src string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(src, "//", " ")), " ")
}

// runInModule generates code, drops it into a temp module, and runs its tests.
func runInModule(t *testing.T, schema string, cfg Config, testSrc string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping compile test in -short mode")
	}
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(cfg, []byte(schema))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	dir := t.TempDir()
	gomod := "module gentest\n\ngo 1.22\n\nrequire github.com/ChristopherDavenport/jsonschema v0.0.0\n\nreplace github.com/ChristopherDavenport/jsonschema => " + repoRoot + "\n"
	writeFile(t, filepath.Join(dir, "go.mod"), gomod)
	writeFile(t, filepath.Join(dir, "schema.go"), string(out))
	writeFile(t, filepath.Join(dir, "schema_test.go"), testSrc)

	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code failed to build/test: %v\n%s\n--- source ---\n%s", err, b, out)
	}
}

const oneOfSchema = `{
  "type": "object",
  "required": ["payment"],
  "properties": {
    "payment": {"oneOf": [{"$ref": "#/$defs/card"}, {"$ref": "#/$defs/bank"}]}
  },
  "$defs": {
    "card": {"type": "object", "required": ["cardNumber"], "properties": {"cardNumber": {"type": "string", "minLength": 12}}},
    "bank": {"type": "object", "required": ["iban"], "properties": {"iban": {"type": "string", "minLength": 15}}}
  }
}`

const oneOfTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestOneOf(t *testing.T) {
	var o Order
	if err := json.Unmarshal([]byte(` + "`" + `{"payment":{"cardNumber":"1234567812345678"}}` + "`" + `), &o); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	if _, ok := o.Payment.(*Card); !ok {
		t.Fatalf("expected *Card, got %T", o.Payment)
	}
	if err := o.Validate(); err != nil {
		t.Fatalf("valid order rejected: %v", err)
	}

	if err := json.Unmarshal([]byte(` + "`" + `{"payment":{"iban":"DE89370400440532013000"}}` + "`" + `), &o); err != nil {
		t.Fatalf("unmarshal bank: %v", err)
	}
	if _, ok := o.Payment.(*Bank); !ok {
		t.Fatalf("expected *Bank, got %T", o.Payment)
	}

	if err := json.Unmarshal([]byte(` + "`" + `{"payment":{"foo":1}}` + "`" + `), &o); err == nil {
		t.Fatal("data matching no variant should fail")
	}
}
`

const semanticTest = `package gentest

import "testing"

func TestGeneratedValidate(t *testing.T) {
	good := Person{ID: "00000000-0000-0000-0000-000000000000", Name: "Al"}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid person rejected: %v", err)
	}
	badName := good
	badName.Name = ""
	if badName.Validate() == nil {
		t.Fatal("empty name should fail minLength")
	}
	badFmt := good
	badFmt.ID = "not-a-uuid"
	if badFmt.Validate() == nil {
		t.Fatal("bad uuid should fail format assertion")
	}
	bad := Person{}
	if err := bad.UnmarshalJSON([]byte("{}")); err == nil {
		t.Fatal("missing required id/name should fail UnmarshalJSON")
	}
}
`

const xgoSchema = `{
  "type": "object",
  "properties": {
    "createdAt": {"type": "string", "format": "date-time", "x-go": {"type": "time.Time", "import": "time"}},
    "labels": {"type": "object", "additionalProperties": {"type": "string"}},
    "counts": {"type": "object", "additionalProperties": {"type": "integer"}}
  }
}`

const xgoTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestXGoMap(t *testing.T) {
	var d Doc
	if err := json.Unmarshal([]byte(` + "`" + `{"createdAt":"2020-06-01T12:00:00Z","labels":{"a":"b"},"counts":{"x":3}}` + "`" + `), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.CreatedAt == nil || d.CreatedAt.Year() != 2020 {
		t.Fatalf("createdAt not a parsed time.Time: %v", d.CreatedAt)
	}
	if d.Labels["a"] != "b" {
		t.Fatalf("labels map wrong: %v", d.Labels)
	}
	if d.Counts["x"] != 3 {
		t.Fatalf("counts map wrong: %v", d.Counts)
	}
}
`

const tupleSchema = `{
  "type": "object",
  "properties": {
    "coordinates": {"type": "array", "prefixItems": [{"type": "number"}, {"type": "number"}], "items": false}
  }
}`

const tupleTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestTuple(t *testing.T) {
	var p Point
	if err := json.Unmarshal([]byte(` + "`" + `{"coordinates":[1.5,2.5]}` + "`" + `), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Coordinates == nil || p.Coordinates.Elem0 != 1.5 || p.Coordinates.Elem1 != 2.5 {
		t.Fatalf("tuple decoded wrong: %+v", p.Coordinates)
	}
	b, err := json.Marshal(p.Coordinates)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != ` + "`" + `[1.5,2.5]` + "`" + ` {
		t.Fatalf("tuple marshaled wrong: %s", b)
	}
	var pc PointCoordinates
	if err := pc.UnmarshalJSON([]byte(` + "`" + `[1]` + "`" + `)); err == nil {
		t.Fatal("too few elements should fail")
	}
}
`

const numSchema = `{
  "type": "object",
  "properties": {
    "score": {"type": "number", "minimum": 0, "maximum": 100, "multipleOf": 0.5},
    "count": {"type": "integer", "minimum": 1, "multipleOf": 2}
  }
}`

const numTest = `package gentest

import "testing"

func TestNumeric(t *testing.T) {
	f, c := 2.5, int64(4)
	d := Numbers{Score: &f, Count: &c}
	if err := d.Validate(); err != nil {
		t.Fatalf("valid numbers rejected: %v", err)
	}
	neg := -1.0
	d.Score = &neg
	if d.Validate() == nil {
		t.Fatal("negative score should fail minimum")
	}
	off := 2.3
	d.Score = &off
	if d.Validate() == nil {
		t.Fatal("2.3 should fail multipleOf 0.5")
	}
	odd := int64(3)
	d.Score = &f
	d.Count = &odd
	if d.Validate() == nil {
		t.Fatal("3 should fail multipleOf 2")
	}
}
`

const condSchema = `{
  "type": "object",
  "properties": {"kind": {"type": "string"}, "value": {"type": "string"}},
  "if": {"properties": {"kind": {"const": "secret"}}},
  "then": {"required": ["value"]}
}`

const condTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestEngineFallback(t *testing.T) {
	mustDecode := func(s string) Item {
		var x Item
		if err := json.Unmarshal([]byte(s), &x); err != nil {
			t.Fatalf("unmarshal %s: %v", s, err)
		}
		return x
	}
	// kind=secret requires value.
	ok := mustDecode(` + "`" + `{"kind":"secret","value":"x"}` + "`" + `)
	if err := ok.Validate(); err != nil {
		t.Fatalf("secret+value should be valid: %v", err)
	}
	bad := mustDecode(` + "`" + `{"kind":"secret"}` + "`" + `)
	if bad.Validate() == nil {
		t.Fatal("secret without value should fail if/then")
	}
	// kind!=secret has no requirement.
	other := mustDecode(` + "`" + `{"kind":"public"}` + "`" + `)
	if err := other.Validate(); err != nil {
		t.Fatalf("non-secret should be valid: %v", err)
	}
}
`

// The same tagged union with the tag written as an `enum` — the most natural
// spelling, and the one that used to fall off the inline path because the field's
// Go type was the generated `ItemKind` rather than a bare `string`.
const enumCondSchema = `{
  "type": "object",
  "properties": {"kind": {"enum": ["secret", "public"]}, "value": {"type": "string"}},
  "if": {"properties": {"kind": {"const": "secret"}}},
  "then": {"required": ["value"]}
}`

const enumCondTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestEnumDiscriminator(t *testing.T) {
	mustDecode := func(s string) Item {
		var x Item
		if err := json.Unmarshal([]byte(s), &x); err != nil {
			t.Fatalf("unmarshal %s: %v", s, err)
		}
		return x
	}
	ok := mustDecode(` + "`" + `{"kind":"secret","value":"x"}` + "`" + `)
	if err := ok.Validate(); err != nil {
		t.Fatalf("secret+value should be valid: %v", err)
	}
	bad := mustDecode(` + "`" + `{"kind":"secret"}` + "`" + `)
	if bad.Validate() == nil {
		t.Fatal("secret without value should fail if/then")
	}
	other := mustDecode(` + "`" + `{"kind":"public"}` + "`" + `)
	if err := other.Validate(); err != nil {
		t.Fatalf("non-secret should be valid: %v", err)
	}
	// The enum still validates its own membership.
	var bogus Item
	if err := json.Unmarshal([]byte(` + "`" + `{"kind":"nope","value":"x"}` + "`" + `), &bogus); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if bogus.Validate() == nil {
		t.Fatal("a value outside the enum should fail")
	}
}
`

// A $ref to a document that is not bundled into the input. Without a way to
// register it, the engine fails at runtime with "cannot resolve $ref" and the
// only fix is to re-bundle the schema by hand.
const remoteRefSchema = `{
  "type": "object",
  "properties": {"kind": {"type": "string"}, "value": {"type": "string"}},
  "dependentSchemas": {"kind": {"$ref": "https://remote.example/r.json"}}
}`

const remoteRefTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestRemoteRefViaAddSchemaResource(t *testing.T) {
	// Registered before the first Validate, which is when the compiler is built.
	AddSchemaResource("https://remote.example/r.json", []byte(` + "`" + `{"required":["value"]}` + "`" + `))

	mustDecode := func(s string) Item {
		var x Item
		if err := json.Unmarshal([]byte(s), &x); err != nil {
			t.Fatalf("unmarshal %s: %v", s, err)
		}
		return x
	}
	ok := mustDecode(` + "`" + `{"kind":"k","value":"v"}` + "`" + `)
	if err := ok.Validate(); err != nil {
		t.Fatalf("the remote ref should resolve and pass: %v", err)
	}
	// The remote schema is actually applied, not merely resolvable.
	bad := mustDecode(` + "`" + `{"kind":"k"}` + "`" + `)
	if bad.Validate() == nil {
		t.Fatal("kind present without value should fail the remote required")
	}
}
`

const allOfSchema = `{
  "allOf": [{"$ref": "#/$defs/base"}, {"$ref": "#/$defs/audit"}],
  "$defs": {
    "base": {"type": "object", "required": ["id"], "properties": {"id": {"type": "string"}, "name": {"type": "string", "minLength": 1}}},
    "audit": {"type": "object", "required": ["createdBy"], "properties": {"createdBy": {"type": "string"}}}
  }
}`

const allOfTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestAllOf(t *testing.T) {
	var r Record
	if err := json.Unmarshal([]byte(` + "`" + `{"id":"1","name":"x","createdBy":"me"}` + "`" + `), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.ID != "1" || r.CreatedBy != "me" { // promoted from Base and Audit
		t.Fatalf("promoted fields wrong: %+v", r)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	if err := json.Unmarshal([]byte(` + "`" + `{"id":"1"}` + "`" + `), &r); err == nil {
		t.Fatal("missing createdBy (from Audit) should fail")
	}
	if err := json.Unmarshal([]byte(` + "`" + `{"createdBy":"me"}` + "`" + `), &r); err == nil {
		t.Fatal("missing id (from Base) should fail")
	}
}
`

// mergeSchema composes an overlapping pair of object members with a free-form
// dictionary member, and adds a property of its own.
const mergeSchema = `{
  "type": "object",
  "properties": {"own": {"type": "string"}},
  "allOf": [{"$ref": "#/$defs/base"}, {"$ref": "#/$defs/audit"}, {"$ref": "#/$defs/extras"}],
  "$defs": {
    "base":   {"type": "object", "required": ["id"], "properties": {"id": {"type": "string"}, "name": {"type": "string"}}},
    "audit":  {"type": "object", "properties": {"id": {"type": "string"}, "createdBy": {"type": "string"}}},
    "extras": {"type": "object", "additionalProperties": {"type": "string"}}
  }
}`

const mergeTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestAllOfMerge(t *testing.T) {
	const doc = ` + "`" + `{"id":"1","name":"n","createdBy":"me","own":"o","extra":"e"}` + "`" + `
	var r Record
	if err := json.Unmarshal([]byte(doc), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Base.ID != "1" || r.Audit.ID == nil || *r.Audit.ID != "1" {
		t.Fatalf("both parts should see the shared property: %+v", r)
	}
	if r.Extras["extra"] != "e" {
		t.Fatalf("dictionary part missed the extra key: %v", r.Extras)
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// One flat object: the shared "id" appears once, the dictionary's entries
	// sit alongside the declared properties, and nothing nests under "Extras".
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("marshal produced invalid JSON %s: %v", b, err)
	}
	for k, want := range map[string]any{"id": "1", "name": "n", "createdBy": "me", "own": "o", "extra": "e"} {
		if got[k] != want {
			t.Errorf("marshaled %s: got[%q] = %v, want %v", b, k, got[k], want)
		}
	}
	if _, nested := got["Extras"]; nested {
		t.Errorf("dictionary part nested under its type name: %s", b)
	}
	if len(got) != 5 {
		t.Errorf("unexpected keys in %s", b)
	}

	// The type reads back its own output — this is what ambiguous promotion broke.
	var back Record
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("re-unmarshal own output %s: %v", b, err)
	}
	if back.Base.ID != "1" || back.Own == nil || *back.Own != "o" {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
}
`

const fallbackSchema = `{
  "type": "object",
  "properties": {"kind": {"type": "string"}, "value": {"type": "string"}},
  "dependentSchemas": {"kind": {"required": ["value"]}}
}`

const fallbackTest = `package gentest

import (
	"encoding/json"
	"testing"
)

func TestEngineFallback(t *testing.T) {
	dec := func(s string) Item {
		var x Item
		if err := json.Unmarshal([]byte(s), &x); err != nil {
			t.Fatalf("unmarshal %s: %v", s, err)
		}
		return x
	}
	ok := dec(` + "`" + `{"kind":"x","value":"y"}` + "`" + `)
	if err := ok.Validate(); err != nil {
		t.Fatalf("kind+value should be valid: %v", err)
	}
	bad := dec(` + "`" + `{"kind":"x"}` + "`" + `)
	if bad.Validate() == nil {
		t.Fatal("kind present without value should fail dependentSchemas")
	}
	none := dec(` + "`" + `{"value":"y"}` + "`" + `)
	if err := none.Validate(); err != nil {
		t.Fatalf("no kind should be valid: %v", err)
	}
}
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
