package xvalid

import (
	"encoding/json"
	"testing"
)

func TestMergeObjects(t *testing.T) {
	raw := func(ss ...string) []json.RawMessage {
		parts := make([]json.RawMessage, len(ss))
		for i, s := range ss {
			parts[i] = json.RawMessage(s)
		}
		return parts
	}

	tests := []struct {
		name  string
		parts []json.RawMessage
		want  string
	}{
		{"disjoint parts keep their order", raw(`{"id":"1"}`, `{"createdBy":"me"}`), `{"id":"1","createdBy":"me"}`},
		{"first writer wins", raw(`{"id":"1","x":"a"}`, `{"id":"2","y":"b"}`), `{"id":"1","x":"a","y":"b"}`},
		{"a map part fills what is left", raw(`{"id":"1"}`, `{"id":"stale","extra":"e"}`), `{"id":"1","extra":"e"}`},
		{"nested values pass through verbatim", raw(`{"a":{"b":[1,2]}}`), `{"a":{"b":[1,2]}}`},
		{"null and empty parts are skipped", raw(`null`, ``, `{"a":1}`), `{"a":1}`},
		{"no parts is an empty object", nil, `{}`},
		{"numbers keep their literal form", raw(`{"n":1.50,"big":12345678901234567890}`), `{"n":1.50,"big":12345678901234567890}`},
		{"escaped keys survive", raw(`{"a\"b":1}`), `{"a\"b":1}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeObjects(tc.parts...)
			if err != nil {
				t.Fatalf("MergeObjects: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("MergeObjects() = %s, want %s", got, tc.want)
			}
			if !json.Valid(got) {
				t.Errorf("MergeObjects() produced invalid JSON: %s", got)
			}
		})
	}
}

func TestMergeObjectsRejectsNonObjects(t *testing.T) {
	for _, part := range []string{`[1,2]`, `"str"`, `{`, `7`} {
		if _, err := MergeObjects(json.RawMessage(`{"a":1}`), json.RawMessage(part)); err == nil {
			t.Errorf("MergeObjects(%s) should fail: it cannot be merged into an object", part)
		}
	}
}
