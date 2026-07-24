package gen

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
