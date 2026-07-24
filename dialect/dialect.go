// Package dialect detects which JSON Schema draft a document targets and
// normalizes older drafts onto the canonical 2020-12 ir model, so the rest of
// the toolchain only ever sees one dialect.
//
// Structural rewrites (definitions->$defs, items-array->prefixItems,
// dependencies split, $recursive*->$dynamic*) happen during ir unmarshaling.
// This package handles the remaining draft-specific reference semantics that
// depend on knowing the draft: draft-07 $id-as-anchor and $ref sibling
// suppression.
package dialect

import (
	"strings"

	"github.com/ChristopherDavenport/jsonschema/ir"
)

// Draft identifies a JSON Schema draft. Values are ordered oldest to newest so
// that feature gates can be written as comparisons.
type Draft int

const (
	Draft4 Draft = iota
	Draft6
	Draft7
	Draft2019
	Draft2020
)

// Detect maps a $schema URI to a Draft. The second result reports whether the
// URI was recognized.
func Detect(schemaURI string) (Draft, bool) {
	switch {
	case schemaURI == "":
		return Draft2020, false
	case strings.Contains(schemaURI, "2020-12"):
		return Draft2020, true
	case strings.Contains(schemaURI, "2019-09"):
		return Draft2019, true
	case strings.Contains(schemaURI, "draft-07"):
		return Draft7, true
	case strings.Contains(schemaURI, "draft-06"):
		return Draft6, true
	case strings.Contains(schemaURI, "draft-04"):
		return Draft4, true
	default:
		return Draft2020, false
	}
}

// Normalize applies draft-specific reference rewrites to s and all subschemas.
func Normalize(s *ir.Schema, d Draft) {
	if s == nil || s.IsBoolean() {
		return
	}
	if d <= Draft7 {
		normalizeLegacyRef(s)
	}
	for _, child := range ir.ChildSchemas(s) {
		Normalize(child, d)
	}
}

// normalizeLegacyRef applies the draft-07 rules: a fragment in $id defines an
// anchor, and a $ref suppresses every sibling keyword.
func normalizeLegacyRef(s *ir.Schema) {
	if i := strings.IndexByte(s.ID, '#'); i >= 0 {
		frag := s.ID[i+1:]
		if frag != "" && !strings.HasPrefix(frag, "/") && s.Anchor == "" {
			s.Anchor = frag
		}
		s.ID = s.ID[:i]
	}
	if s.Ref != "" {
		// In draft-07 a $ref suppresses every sibling, including a sibling $id:
		// the $id must not change the base URI used to resolve the $ref.
		s.IgnoreSiblings = true
		s.ID = ""
	}
}
