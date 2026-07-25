package gen

import (
	"fmt"
	"strings"
)

// Report lists what the generated code does not enforce: every declaration
// carrying a NOTE, and for each, what to do about it. It mirrors the comments in
// the output exactly, so a caller (the CLI, a build script) can surface the same
// information at generation time instead of only in the emitted source.
type Report struct {
	Types []TypeReport
}

// TypeReport is one generated type with unenforced constraints.
type TypeReport struct {
	// Type is the generated Go type name.
	Type string
	// HasValidate reports whether the type has a Validate method at all; when
	// false (an alias or union interface) the engine fallback cannot apply.
	HasValidate bool
	// Delegates reports whether this type's Validate delegates to the runtime
	// engine, i.e. -engine-fallback was on and applied to it.
	Delegates bool
	// Items are the keywords this type uses but does not enforce.
	Items []Unenforced
}

// Unenforced is one keyword a generated type does not enforce.
type Unenforced struct {
	// Keyword is the JSON Schema keyword: "not", "dependentSchemas",
	// "if/then/else", "allOf".
	Keyword string
	// Detail is Keyword plus, for shapes with several recognition rules, which
	// rule the schema missed.
	Detail string
	// FallbackWouldEnforce reports whether regenerating with -engine-fallback
	// would enforce this keyword faithfully.
	FallbackWouldEnforce bool
	// Why explains, when FallbackWouldEnforce is false, what stops it.
	Why string
	// Properties are the property names this keyword constrains that the Go type
	// cannot hold, and so cannot survive the fallback's marshal round trip.
	Properties []string
}

// Empty reports whether everything in the document is enforced.
func (r *Report) Empty() bool { return r == nil || len(r.Types) == 0 }

// FallbackWouldFix counts the items that regenerating with -engine-fallback
// would enforce.
func (r *Report) FallbackWouldFix() int {
	n := 0
	for _, t := range r.Types {
		for _, it := range t.Items {
			if it.FallbackWouldEnforce {
				n++
			}
		}
	}
	return n
}

// String renders the report for a terminal: one block per type, one line per
// unenforced keyword, and the remedy beneath it.
func (r *Report) String() string {
	if r.Empty() {
		return ""
	}
	var b strings.Builder
	items := 0
	for _, t := range r.Types {
		items += len(t.Items)
	}
	fmt.Fprintf(&b, "%s in %s not enforced by the generated code:\n",
		plural(items, "constraint", "constraints"), plural(len(r.Types), "type", "types"))

	for _, t := range r.Types {
		fmt.Fprintf(&b, "\n  %s%s\n", t.Type, t.suffix())
		for _, it := range t.Items {
			fmt.Fprintf(&b, "    - %s\n", it.Detail)
			for _, line := range wrapComment(it.remedy()) {
				fmt.Fprintf(&b, "      %s\n", line)
			}
		}
	}

	if n := r.FallbackWouldFix(); n > 0 {
		fmt.Fprintf(&b, "\nRegenerating with -engine-fallback would enforce %s.\n",
			plural(n, "constraint", "constraints"))
	}
	return b.String()
}

// suffix labels how the type is validated, so the reader knows why a remedy
// applies to it.
func (t TypeReport) suffix() string {
	switch {
	case !t.HasValidate:
		return " (no Validate method: not a struct or enum)"
	case t.Delegates:
		return " (Validate delegates to the runtime engine)"
	}
	return ""
}

// remedy is the one-sentence "what to do" for an item.
func (u Unenforced) remedy() string {
	if u.FallbackWouldEnforce {
		return "fix: regenerate with -engine-fallback, or validate the document with the jsonschema engine"
	}
	why := u.Why
	if len(u.Properties) > 0 {
		why += " (" + quoteList(u.Properties) + ")"
	}
	return "fix: validate the document with the jsonschema engine — -engine-fallback cannot enforce this: " + why
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
