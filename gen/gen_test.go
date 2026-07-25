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

// TestGeneratedEngineFallback proves that with EngineFallback, a keyword that is
// never mirrored inline (dependentSchemas) is enforced via the runtime engine.
func TestGeneratedEngineFallback(t *testing.T) {
	runInModule(t, fallbackSchema, Config{Package: "gentest", RootName: "Item", EngineFallback: true}, fallbackTest)
}

// TestGeneratedAllOf proves allOf is modeled as struct embedding with each
// part's required fields and Validate enforced.
func TestGeneratedAllOf(t *testing.T) {
	runInModule(t, allOfSchema, Config{Package: "gentest", RootName: "Record"}, allOfTest)
}

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
		name:    "allOf member is a dictionary, not a struct",
		schema:  `{"allOf":[{"$ref":"#/$defs/a"},{"$ref":"#/$defs/dict"}],"$defs":{"a":{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}},"dict":{"type":"object","additionalProperties":{"type":"string"}}}}`,
		absent:  "\tDict\n",
		note:    "allOf: member 2 is an object schema with no declared properties",
		comment: "embedding a named map type would marshal as {\"Dict\":{…}}",
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
