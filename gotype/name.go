package gotype

import (
	"strings"
	"unicode"
)

// commonInitialisms are uppercased wholesale when they form a word, matching Go
// naming conventions. Callers extend this set via Config.Initialisms for
// domain-specific acronyms.
var commonInitialisms = map[string]bool{
	"ID": true, "URI": true, "URL": true, "API": true, "HTTP": true,
	"HTTPS": true, "JSON": true, "XML": true, "HTML": true, "UUID": true,
	"SQL": true, "IP": true, "TCP": true, "UDP": true, "TLS": true, "DB": true,
	"CPU": true, "RAM": true, "USB": true, "LED": true, "UI": true, "OS": true,
	"MAC": true, "SHA": true, "ECC": true, "AES": true, "DES": true,
	"HMAC": true, "CRC": true, "ASCII": true, "RSA": true, "PDF": true,
	"QR": true, "RFID": true, "NFC": true,
}

// GoName converts an arbitrary JSON name into an exported Go identifier using
// the built-in initialism set.
func GoName(s string) string { return goNameWith(s, nil) }

// GoNameWith is GoName with additional initialisms (e.g. domain acronyms), so a
// driver can compute identifiers that match those Analyze produces for the same
// Config.Initialisms.
func GoNameWith(s string, initialisms []string) string {
	return goNameWith(s, mergeInitialisms(initialisms))
}

// goNameWith converts a JSON name into an exported Go identifier, honoring the
// built-in initialisms plus any extra set. Word boundaries are separators
// (_ - . space /) and camelCase/PascalCase humps; a word's existing internal
// casing is preserved (only its first rune is capitalized) so embedded acronyms
// such as the "IO" in "chipIO" or the "JIS" in "track1JIS" survive.
func goNameWith(s string, extra map[string]bool) string {
	var b strings.Builder
	for _, w := range splitWords(s) {
		b.WriteString(exportWord(w, extra))
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

// exportWord capitalizes a single word: a whole-word initialism becomes all
// uppercase; otherwise the first rune is upper-cased and the remainder kept as
// written.
func exportWord(w string, extra map[string]bool) string {
	if w == "" {
		return ""
	}
	if up := strings.ToUpper(w); commonInitialisms[up] || extra[up] {
		return up
	}
	r := []rune(w)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// mergeInitialisms builds a set (upper-cased) from extra initialism words, or
// nil when there are none.
func mergeInitialisms(extra []string) map[string]bool {
	if len(extra) == 0 {
		return nil
	}
	m := make(map[string]bool, len(extra))
	for _, w := range extra {
		m[strings.ToUpper(w)] = true
	}
	return m
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// splitWords breaks a camelCase / PascalCase / separator-delimited name into
// word segments: "serviceURI" -> ["service","URI"], "chipT0" -> ["chip","T0"],
// "EMVClessConfigure" -> ["EMV","Cless","Configure"].
func splitWords(s string) []string {
	var words []string
	runes := []rune(s)
	n := len(runes)
	start := 0
	flush := func(end int) {
		if end > start {
			words = append(words, string(runes[start:end]))
		}
	}
	for i := 0; i < n; i++ {
		r := runes[i]
		if r == '_' || r == '-' || r == '.' || r == ' ' || r == '/' {
			flush(i)
			start = i + 1
			continue
		}
		if i > start {
			prev := runes[i-1]
			switch {
			case (isLower(prev) || isDigit(prev)) && isUpper(r):
				// lower/digit -> upper: start of a new word ("serviceURI").
				flush(i)
				start = i
			case isUpper(prev) && isUpper(r) && i+1 < n && isLower(runes[i+1]):
				// end of an acronym run before a new word ("EMVCless").
				flush(i)
				start = i
			}
		}
	}
	flush(n)
	return words
}
