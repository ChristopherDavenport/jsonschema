package xvalid

import (
	"strings"
	"testing"
)

// TestCompilePatternTranslatesUnicodeNames pins the one adaptation the default
// engine makes: ECMA-262 spells Unicode general categories with long names, RE2
// only with short codes.
func TestCompilePatternTranslatesUnicodeNames(t *testing.T) {
	re, err := CompilePattern(`^\p{Letter}+$`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !re.MatchString("abc") {
		t.Error(`\p{Letter} should match letters`)
	}
	if re.MatchString("123") {
		t.Error(`\p{Letter} should not match digits`)
	}
}

// TestRE2RejectsLookaround documents the default engine's inherent limit: RE2
// has no lookaround, and no rewriting can add it.
func TestRE2RejectsLookaround(t *testing.T) {
	if _, err := CompilePattern(`(?<=a)b`); err == nil {
		t.Fatal("RE2 should reject lookaround")
	}
}

// stubRegexp matches any string containing its needle. It stands in for a real
// ECMA-262 engine, which the library deliberately does not depend on.
type stubRegexp struct{ needle string }

func (s stubRegexp) MatchString(v string) bool { return strings.Contains(v, s.needle) }

// TestUseRegexpEngine proves `pattern` can be backed by a different engine, so a
// schema using lookaround or backreferences is not a dead end: the caller can
// supply an ECMA-262 engine rather than being told Go cannot do it.
func TestUseRegexpEngine(t *testing.T) {
	t.Cleanup(func() { UseRegexpEngine(nil) })

	var got []string
	UseRegexpEngine(func(p string) (Regexp, error) {
		got = append(got, p)
		// A pattern RE2 could never compile is fine here.
		return stubRegexp{needle: "b"}, nil
	})

	re, err := CompilePattern(`(?<=a)b`)
	if err != nil {
		t.Fatalf("the custom engine should accept lookaround: %v", err)
	}
	if !re.MatchString("ab") || re.MatchString("ac") {
		t.Error("the custom engine's matcher was not used")
	}
	if len(got) != 1 || got[0] != `(?<=a)b` {
		t.Errorf("engine saw %v, want the raw pattern", got)
	}

	// Passing nil restores RE2, so one test cannot leak into the next.
	UseRegexpEngine(nil)
	if _, err := CompilePattern(`(?<=a)b`); err == nil {
		t.Fatal("nil should restore the default RE2 engine")
	}
}
