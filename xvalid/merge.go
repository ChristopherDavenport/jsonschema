package xvalid

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MergeObjects flattens several JSON objects into one, which is how a type
// composed by `allOf` marshals: each part is marshaled on its own and the
// results are merged, instead of relying on Go's field promotion.
//
// The first part to write a key wins, and keys appear in the order they were
// first seen — so callers pass parts in precedence order. Two properties follow
// from that, and are the reason this exists:
//
//   - A property declared by more than one part is emitted once, rather than
//     dropped as an ambiguous promoted field.
//   - A part whose Go type is a map (a free-form `additionalProperties` member)
//     contributes its entries to the same object, rather than nesting them under
//     the embedded type's name.
//
// A nil or JSON-null part contributes nothing. A part that is not a JSON object
// is an error, since it cannot be merged into one.
func MergeObjects(parts ...json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	seen := make(map[string]bool)
	for _, part := range parts {
		if len(part) == 0 || string(part) == "null" {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(part))
		dec.UseNumber()

		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("xvalid: merge objects: %w", err)
		}
		if d, ok := tok.(json.Delim); !ok || d != '{' {
			return nil, fmt.Errorf("xvalid: merge objects: part is %s, want a JSON object", part)
		}

		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("xvalid: merge objects: %w", err)
			}
			key, ok := tok.(string)
			if !ok {
				return nil, fmt.Errorf("xvalid: merge objects: object key is %v, want a string", tok)
			}
			var value json.RawMessage
			if err := dec.Decode(&value); err != nil {
				return nil, fmt.Errorf("xvalid: merge objects: value for %q: %w", key, err)
			}
			if seen[key] {
				continue // an earlier part already wrote this property
			}
			seen[key] = true

			if len(seen) > 1 {
				buf.WriteByte(',')
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return nil, fmt.Errorf("xvalid: merge objects: key %q: %w", key, err)
			}
			buf.Write(encoded)
			buf.WriteByte(':')
			buf.Write(value)
		}
		// More() stops on malformed input as well as at the closing brace, so
		// read the brace to tell a finished object from a truncated one.
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("xvalid: merge objects: %w", err)
		}
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}
