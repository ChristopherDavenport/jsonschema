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

// CompilePattern compiles a JSON Schema `pattern`, adapting the few ECMA-262
// constructs (long Unicode property names) that RE2 spells differently. It does
// not — and cannot — support constructs RE2 lacks entirely, such as lookaround
// or backreferences.
func CompilePattern(p string) (*regexp.Regexp, error) {
	return regexp.Compile(translatePattern(p))
}

// MustCompilePattern is like CompilePattern but panics on error. Generated code
// uses it for schema patterns validated at generation time.
func MustCompilePattern(p string) *regexp.Regexp {
	return regexp.MustCompile(translatePattern(p))
}
