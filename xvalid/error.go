// Package xvalid provides the small, dependency-free runtime that both the
// validation engine and generated code rely on: JSON value-equality, semantic
// format checks, and a structured validation error type.
//
// It imports only the standard library, so generated code that calls into it
// carries no third-party dependencies.
package xvalid

import (
	"strings"
)

// Error is a single validation failure, optionally aggregating nested causes.
// InstanceLocation and KeywordLocation are RFC 6901 JSON Pointers into the
// instance and the schema, respectively.
type Error struct {
	InstanceLocation string
	KeywordLocation  string
	Keyword          string
	Message          string
	Causes           []*Error
}

// Error renders the failure and any nested causes as an indented tree.
func (e *Error) Error() string {
	var b strings.Builder
	e.write(&b, 0)
	return strings.TrimRight(b.String(), "\n")
}

func (e *Error) write(b *strings.Builder, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
	loc := e.InstanceLocation
	if loc == "" {
		loc = "/"
	}
	b.WriteString(loc)
	b.WriteString(": ")
	b.WriteString(e.Message)
	b.WriteByte('\n')
	for _, c := range e.Causes {
		c.write(b, depth+1)
	}
}

// Add appends a cause to e and returns e for chaining.
func (e *Error) Add(cause *Error) *Error {
	if cause != nil {
		e.Causes = append(e.Causes, cause)
	}
	return e
}
