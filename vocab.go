package jsonschema

import (
	"strings"

	"github.com/ChristopherDavenport/jsonschema/ir"
)

// vocabSet records which standard vocabulary groups are active for a dialect.
// A false value means the corresponding keywords are collected as annotations
// (i.e. ignored) rather than asserted.
type vocabSet struct {
	validation  bool
	applicator  bool
	unevaluated bool
}

// allVocab is the default when a dialect declares no $vocabulary: everything on.
var allVocab = vocabSet{validation: true, applicator: true, unevaluated: true}

// activeVocab determines the vocabularies in effect for the root schema, by
// reading the $vocabulary declaration of its $schema meta-schema (if that
// meta-schema is registered). Absent that information, all vocabularies are on.
func (s *Schema) activeVocab() vocabSet {
	uri := s.root.SchemaURI
	if uri == "" {
		return allVocab
	}
	meta := s.lookupMeta(uri)
	if meta == nil || len(meta.Vocabulary) == 0 {
		return allVocab
	}
	var vs vocabSet
	var sawUnevaluated bool
	for vocabURI := range meta.Vocabulary {
		switch {
		case strings.Contains(vocabURI, "/vocab/validation"):
			vs.validation = true
		case strings.Contains(vocabURI, "/vocab/applicator"):
			vs.applicator = true
		case strings.Contains(vocabURI, "/vocab/unevaluated"):
			vs.unevaluated = true
			sawUnevaluated = true
		}
	}
	// Before 2020-12 there is no separate unevaluated vocabulary; the
	// unevaluated* keywords live in the applicator vocabulary.
	if !sawUnevaluated {
		vs.unevaluated = vs.applicator
	}
	return vs
}

func (s *Schema) lookupMeta(uri string) *ir.Schema {
	res := s.ldr.Resources()
	if m, ok := res[uri+"#"]; ok {
		return m
	}
	if m, ok := res[uri]; ok {
		return m
	}
	return nil
}
