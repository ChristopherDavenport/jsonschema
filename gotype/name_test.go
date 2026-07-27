package gotype

import (
	"slices"
	"testing"
)

func TestGoName(t *testing.T) {
	cases := []struct{ in, want string }{
		// A word's internal casing is preserved, so embedded acronyms survive.
		{"chipIO", "ChipIO"},
		{"track1JIS", "Track1JIS"},
		{"EMVClessConfigure", "EMVClessConfigure"},
		// Trailing acronym runs uppercase via the built-in initialism set.
		{"serviceURI", "ServiceURI"},
		{"userId", "UserID"},
		{"http-response", "HTTPResponse"},
		// Separators split words.
		{"user_id", "UserID"},
		{"kebab-case-name", "KebabCaseName"},
		{"snake_case_name", "SnakeCaseName"},
		{"with.dots", "WithDots"},
		// Edge cases.
		{"id", "ID"},
		{"1st", "X1st"}, // may not start with a digit
		{"", "X"},       // never empty
	}
	for _, tc := range cases {
		if got := GoName(tc.in); got != tc.want {
			t.Errorf("GoName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGoNameWith(t *testing.T) {
	// A domain initialism supplied via Config.Initialisms is upper-cased wholesale.
	if got := GoNameWith("pinBlock", []string{"PIN"}); got != "PINBlock" {
		t.Errorf(`GoNameWith("pinBlock", [PIN]) = %q, want "PINBlock"`, got)
	}
	// Matching is case-insensitive on the supplied set.
	if got := GoNameWith("emvData", []string{"emv"}); got != "EMVData" {
		t.Errorf(`GoNameWith("emvData", [emv]) = %q, want "EMVData"`, got)
	}
	// Without the extra set, the same word keeps its written casing.
	if got := GoName("pinBlock"); got != "PinBlock" {
		t.Errorf(`GoName("pinBlock") = %q, want "PinBlock"`, got)
	}
}

func TestSplitWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"serviceURI", []string{"service", "URI"}},
		{"chipT0", []string{"chip", "T0"}},
		{"EMVClessConfigure", []string{"EMV", "Cless", "Configure"}},
		{"track1JIS", []string{"track1", "JIS"}},
		{"a_b-c.d e/f", []string{"a", "b", "c", "d", "e", "f"}},
	}
	for _, tc := range cases {
		if got := splitWords(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("splitWords(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
