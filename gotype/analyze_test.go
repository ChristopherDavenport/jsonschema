package gotype

import (
	"encoding/json"
	"testing"

	"github.com/ChristopherDavenport/jsonschema/ir"
)

func TestIsPlainScalar(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   bool
	}{
		{"string", `{"type":"string"}`, true},
		{"integer", `{"type":"integer"}`, true},
		{"nullable string", `{"type":["string","null"]}`, true},
		// Value constraints do not disqualify: inlining discards them, matching
		// that a named scalar alias carries no Validate of its own either.
		{"constrained string", `{"type":"string","pattern":"^x","maxLength":3}`, true},
		{"enum", `{"type":"string","enum":["a","b"]}`, false},
		{"const", `{"const":"a"}`, false},
		{"ref", `{"$ref":"#/$defs/x"}`, false},
		{"object", `{"type":"object","properties":{"a":{"type":"string"}}}`, false},
		{"array", `{"type":"array","items":{"type":"string"}}`, false},
		{"union of two non-null types", `{"type":["string","integer"]}`, false},
		{"no type", `{"description":"x"}`, false},
	}
	for _, tc := range cases {
		var s ir.Schema
		if err := json.Unmarshal([]byte(tc.schema), &s); err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		if got := IsPlainScalar(&s); got != tc.want {
			t.Errorf("%s: IsPlainScalar(%s) = %v, want %v", tc.name, tc.schema, got, tc.want)
		}
	}
}
