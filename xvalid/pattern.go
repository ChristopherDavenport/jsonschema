package xvalid

import "regexp"

// Go's regexp is RE2, which recognizes Unicode general categories only by their
// short codes (`\p{L}`), while the JSON Schema `pattern` dialect is ECMA-262,
// which also accepts the long names (`\p{Letter}`). translatePattern rewrites
// the long names to their short codes so such patterns compile; script names
// and short codes are left untouched.
var unicodeLongNames = map[string]string{
	"Letter": "L", "Uppercase_Letter": "Lu", "Lowercase_Letter": "Ll",
	"Titlecase_Letter": "Lt", "Modifier_Letter": "Lm", "Other_Letter": "Lo",
	"Mark": "M", "Nonspacing_Mark": "Mn", "Spacing_Mark": "Mc",
	"Spacing_Combining_Mark": "Mc", "Enclosing_Mark": "Me",
	"Number": "N", "Decimal_Number": "Nd", "Letter_Number": "Nl", "Other_Number": "No",
	"Punctuation": "P", "Connector_Punctuation": "Pc", "Dash_Punctuation": "Pd",
	"Open_Punctuation": "Ps", "Close_Punctuation": "Pe", "Initial_Punctuation": "Pi",
	"Final_Punctuation": "Pf", "Other_Punctuation": "Po",
	"Symbol": "S", "Math_Symbol": "Sm", "Currency_Symbol": "Sc",
	"Modifier_Symbol": "Sk", "Other_Symbol": "So",
	"Separator": "Z", "Space_Separator": "Zs", "Line_Separator": "Zl", "Paragraph_Separator": "Zp",
	"Other": "C", "Control": "Cc", "Format": "Cf", "Surrogate": "Cs",
	"Private_Use": "Co", "Unassigned": "Cn",
}

var unicodePropRe = regexp.MustCompile(`\\([pP])\{([A-Za-z_]+)\}`)

func translatePattern(p string) string {
	return unicodePropRe.ReplaceAllStringFunc(p, func(m string) string {
		sub := unicodePropRe.FindStringSubmatch(m)
		if short, ok := unicodeLongNames[sub[2]]; ok {
			return `\` + sub[1] + `{` + short + `}`
		}
		return m
	})
}

// Regexp is the part of a compiled pattern the validator uses. *regexp.Regexp
// satisfies it, as do third-party engines such as dlclark/regexp2.
type Regexp interface {
	MatchString(s string) bool
}

// RegexpEngine compiles one JSON Schema `pattern` into a matcher.
type RegexpEngine func(pattern string) (Regexp, error)

// re2Engine is the default: Go's own RE2, with the ECMA-262 long Unicode
// property names rewritten to the short codes RE2 understands.
func re2Engine(p string) (Regexp, error) {
	return regexp.Compile(translatePattern(p))
}

var engine RegexpEngine = re2Engine

// UseRegexpEngine replaces the engine used for `pattern` and the `regex` format.
// Passing nil restores the default.
//
// RE2 has no lookaround and no backreferences, and no rewriting can add them, so
// patterns using those fail to compile under the default engine. Supplying an
// ECMA-262 engine (for example one backed by dlclark/regexp2) makes them work,
// at the cost of RE2's linear-time matching guarantee — a regex engine that
// backtracks can be made to run for a very long time on hostile input, which
// matters when the schema or the data is untrusted.
//
// Not safe for concurrent use with validation: set it during initialization,
// before the first Validate or Compile call.
func UseRegexpEngine(e RegexpEngine) {
	if e == nil {
		e = re2Engine
	}
	engine = e
}

// CompilePattern compiles a JSON Schema `pattern` with the active engine.
func CompilePattern(p string) (Regexp, error) {
	return engine(p)
}

// MustCompilePattern is like CompilePattern but panics on error. Generated code
// uses it for schema patterns validated at generation time.
func MustCompilePattern(p string) Regexp {
	re, err := CompilePattern(p)
	if err != nil {
		panic(err)
	}
	return re
}
