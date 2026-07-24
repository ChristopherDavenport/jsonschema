package gotype

import (
	"strings"
	"unicode"
)

// commonInitialisms are uppercased wholesale when they form a word, matching
// Go naming conventions (golint's list, trimmed to the common cases).
var commonInitialisms = map[string]bool{
	"ID": true, "URL": true, "URI": true, "API": true, "HTTP": true,
	"JSON": true, "XML": true, "HTML": true, "UUID": true, "SQL": true,
	"IP": true, "TCP": true, "UDP": true, "TLS": true, "DB": true,
}

// GoName converts an arbitrary JSON name into an exported Go identifier.
func GoName(s string) string {
	words := splitWords(s)
	var b strings.Builder
	for _, w := range words {
		up := strings.ToUpper(w)
		if commonInitialisms[up] {
			b.WriteString(up)
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]) + strings.ToLower(w[1:]))
	}
	out := b.String()
	if out == "" {
		return "X"
	}
	// An identifier may not start with a digit.
	if unicode.IsDigit(rune(out[0])) {
		out = "X" + out
	}
	return out
}

// splitWords breaks a string on non-alphanumeric boundaries and camelCase humps.
func splitWords(s string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	var prev rune
	for i, r := range s {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ' || r == '/':
			flush()
		case unicode.IsUpper(r) && i > 0 && unicode.IsLower(prev):
			flush()
			cur.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur.WriteRune(r)
		default:
			flush()
		}
		prev = r
	}
	flush()
	return words
}
