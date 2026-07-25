// Package jsonschema compiles JSON Schema documents and validates decoded JSON
// values against them. It accepts draft 2020-12, 2019-09 and draft-07 (all
// normalized to a canonical 2020-12 model) and is the conformance-anchored core
// that the code generator mirrors.
package jsonschema

import (
	"encoding/json"
	"fmt"

	"github.com/ChristopherDavenport/jsonschema/dialect"
	"github.com/ChristopherDavenport/jsonschema/ir"
	"github.com/ChristopherDavenport/jsonschema/loader"
	"github.com/ChristopherDavenport/jsonschema/metaschema"
	"github.com/ChristopherDavenport/jsonschema/xvalid"
)

// Compiler accumulates schema resources and compiles them into validators.
type Compiler struct {
	ldr          *loader.Loader
	assertFormat bool
	defaultDraft dialect.Draft
}

// NewCompiler returns an empty Compiler.
func NewCompiler() *Compiler {
	return &Compiler{ldr: loader.New(), defaultDraft: dialect.Draft2020}
}

// DefaultDraft sets the draft assumed for documents without a $schema keyword.
// It returns the compiler for chaining.
func (c *Compiler) DefaultDraft(d dialect.Draft) *Compiler {
	c.defaultDraft = d
	return c
}

// AssertFormat controls whether the `format` keyword is enforced as an
// assertion (true) or collected as an annotation only (false, the spec
// default). It returns the compiler for chaining.
func (c *Compiler) AssertFormat(on bool) *Compiler {
	c.assertFormat = on
	return c
}

// RegisterMetaSchemas registers the bundled official meta-schemas so that a
// document referencing one (e.g. for self-validation) resolves offline. It
// returns the compiler for chaining.
func (c *Compiler) RegisterMetaSchemas() (*Compiler, error) {
	docs, err := metaschema.Documents()
	if err != nil {
		return c, err
	}
	for uri, data := range docs {
		if err := c.AddResource(uri, data); err != nil {
			return c, fmt.Errorf("register meta-schema %q: %w", uri, err)
		}
	}
	return c, nil
}

// AddResource parses a JSON schema document and registers it (and its
// subschemas) under the given canonical URI, so later references can resolve to
// it. Call this for the root schema and for any remote documents it references.
func (c *Compiler) AddResource(uri string, data []byte) error {
	_, err := c.parseAndRegister(uri, data)
	return err
}

// parseAndRegister decodes data, normalizes it for its draft, and registers it.
func (c *Compiler) parseAndRegister(uri string, data []byte) (*ir.Schema, error) {
	var s ir.Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("jsonschema: parse %q: %w", uri, err)
	}
	draft := c.defaultDraft
	if d, ok := dialect.Detect(s.SchemaURI); ok {
		draft = d
	}
	dialect.Normalize(&s, draft)
	c.ldr.AddSchema(uri, &s)
	return &s, nil
}

// Compile returns a validator for a previously added resource URI.
func (c *Compiler) Compile(uri string) (*Schema, error) {
	res := c.ldr.Resources()
	root, ok := res[uri+"#"]
	if !ok {
		root, ok = res[uri]
	}
	if !ok {
		return nil, fmt.Errorf("jsonschema: no compiled resource at %q", uri)
	}
	return &Schema{root: root, ldr: c.ldr, assertFormat: c.assertFormat}, nil
}

// AddAndCompile parses data, registers it under uri, and returns a validator
// for its root — even if a top-level $id relocated the root's canonical URI.
func (c *Compiler) AddAndCompile(uri string, data []byte) (*Schema, error) {
	root, err := c.parseAndRegister(uri, data)
	if err != nil {
		return nil, err
	}
	return &Schema{root: root, ldr: c.ldr, assertFormat: c.assertFormat}, nil
}

// Compile is a convenience that compiles a single self-contained schema
// document with no external references.
func Compile(data []byte) (*Schema, error) {
	return NewCompiler().AddAndCompile("", data)
}

// Schema is a compiled schema ready to validate instances.
type Schema struct {
	root         *ir.Schema
	ldr          *loader.Loader
	assertFormat bool
}

// Validate reports whether instance (a value decoded by encoding/json into an
// any: nil, bool, float64, string, []any, map[string]any) satisfies the schema.
// A non-nil error describes every failure.
func (s *Schema) Validate(instance any) error {
	v := &validator{
		ldr:          s.ldr,
		assertFormat: s.assertFormat,
		patterns:     map[string]xvalid.Regexp{},
		vocab:        s.activeVocab(),
	}
	if _, err := v.validate(s.root, instance, "", ""); err != nil {
		return err
	}
	return nil
}
