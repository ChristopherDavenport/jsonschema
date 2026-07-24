package jsonschema

import (
	"bytes"
	"encoding/json"
	"testing"
)

// mustInstance decodes JSON into an any with UseNumber, matching how the engine
// expects instances to be decoded for exact numeric comparison.
func mustInstance(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode instance %q: %v", s, err)
	}
	return v
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		instance string
		valid    bool
	}{
		{"type ok", `{"type":"string"}`, `"hi"`, true},
		{"type bad", `{"type":"string"}`, `42`, false},
		{"minLength ok", `{"type":"string","minLength":3}`, `"abc"`, true},
		{"minLength bad", `{"type":"string","minLength":3}`, `"ab"`, false},
		{"multipleOf decimal", `{"multipleOf":0.1}`, `0.3`, true},
		{"exclusiveMinimum boundary", `{"exclusiveMinimum":1.1}`, `1.1`, false},
		{"enum ok", `{"enum":[1,"a",null]}`, `"a"`, true},
		{"enum bad", `{"enum":[1,"a",null]}`, `2`, false},
		{"const null present", `{"const":null}`, `null`, true},
		{"const null mismatch", `{"const":null}`, `false`, false},
		{"uniqueItems bad", `{"uniqueItems":true}`, `[1,1]`, false},
		{"required missing", `{"required":["a"]}`, `{}`, false},
		{"oneOf exactly one", `{"oneOf":[{"type":"string"},{"type":"number"}]}`, `"x"`, true},
		{"oneOf matches two", `{"oneOf":[{"type":"number"},{"minimum":0}]}`, `5`, false},
		{"if-then", `{"if":{"const":"x"},"then":{"minLength":5}}`, `"x"`, false},
		{"prefixItems tuple", `{"prefixItems":[{"type":"string"},{"type":"number"}]}`, `["a",1]`, true},
		{"prefixItems tuple bad", `{"prefixItems":[{"type":"string"},{"type":"number"}]}`, `["a","b"]`, false},
		{"boolean false", `false`, `1`, false},
		{"boolean true", `true`, `1`, true},
		{"dependentRequired", `{"dependentRequired":{"a":["b"]}}`, `{"a":1}`, false},
		{"unevaluatedProperties", `{"properties":{"a":{}},"unevaluatedProperties":false}`, `{"a":1,"b":2}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Compile([]byte(tc.schema))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			err = s.Validate(mustInstance(t, tc.instance))
			if got := err == nil; got != tc.valid {
				t.Fatalf("valid=%v, want %v (err=%v)", got, tc.valid, err)
			}
		})
	}
}

func TestFormatAssertionOptIn(t *testing.T) {
	schema := []byte(`{"type":"string","format":"uuid"}`)
	bad := mustInstance(t, `"not-a-uuid"`)

	// Default: format is annotation-only, so a bad uuid still validates.
	s, err := Compile(schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(bad); err != nil {
		t.Fatalf("format should be annotation-only by default, got %v", err)
	}

	// Opt in: format is asserted, so a bad uuid is rejected.
	c := NewCompiler().AssertFormat(true)
	sa, err := c.AddAndCompile("", schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := sa.Validate(bad); err == nil {
		t.Fatal("expected format assertion failure")
	}
}

func TestRefResolution(t *testing.T) {
	schema := []byte(`{
		"$defs": {"pos": {"type":"integer","minimum":0}},
		"properties": {"n": {"$ref": "#/$defs/pos"}}
	}`)
	s, err := Compile(schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(mustInstance(t, `{"n": 5}`)); err != nil {
		t.Fatalf("want valid: %v", err)
	}
	if err := s.Validate(mustInstance(t, `{"n": -1}`)); err == nil {
		t.Fatal("want invalid for negative")
	}
}
