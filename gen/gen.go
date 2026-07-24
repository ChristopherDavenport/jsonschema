// Package gen turns a JSON Schema document into Go source: named types plus
// Validate methods whose inline checks mirror the jsonschema validation engine,
// so the generated code enforces the schema with no third-party runtime
// dependency (only the first-party xvalid helpers).
package gen

import (
	"encoding/json"
	"fmt"

	"github.com/ChristopherDavenport/jsonschema/dialect"
	"github.com/ChristopherDavenport/jsonschema/gotype"
	"github.com/ChristopherDavenport/jsonschema/ir"
	"github.com/ChristopherDavenport/jsonschema/loader"
)

// Config controls code generation.
type Config struct {
	// Package is the generated package name.
	Package string
	// RootName is the Go type name for the root schema.
	RootName string
	// BaseURI is the retrieval URI used to resolve references in the document.
	BaseURI string
	// AssertFormat emits `format` checks in Validate methods when true.
	AssertFormat bool
	// DefaultDraft is assumed when the document has no $schema keyword.
	DefaultDraft dialect.Draft
	// EngineFallback makes Validate for types that use keywords the generator
	// cannot mirror inline (if/then/else, dependentSchemas, not, allOf) delegate
	// to the embedded schema + runtime engine, trading a dependency on the
	// jsonschema package for full conformance.
	EngineFallback bool
}

// Generate parses a schema document and returns formatted Go source.
func Generate(cfg Config, data []byte) ([]byte, error) {
	var s ir.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("gen: parse schema: %w", err)
	}

	draft := cfg.DefaultDraft
	if d, ok := dialect.Detect(s.SchemaURI); ok {
		draft = d
	}
	dialect.Normalize(&s, draft)

	ldr := loader.New()
	ldr.AddSchema(cfg.BaseURI, &s)

	model, err := gotype.Analyze(&s, ldr, gotype.Config{
		Package:  cfg.Package,
		RootName: cfg.RootName,
	})
	if err != nil {
		return nil, err
	}

	return (&emitter{
		cfg:         cfg,
		model:       model,
		docBytes:    data,
		hasValidate: map[string]bool{},
		isInterface: map[string]bool{},
	}).emit()
}
