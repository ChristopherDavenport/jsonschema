package jsonschema

import (
	"encoding/json"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strings"

	"github.com/ChristopherDavenport/jsonschema/ir"
	"github.com/ChristopherDavenport/jsonschema/loader"
	"github.com/ChristopherDavenport/jsonschema/xvalid"
)

// validator carries the state shared across a single Validate call.
type validator struct {
	ldr          *loader.Loader
	assertFormat bool
	patterns     map[string]*regexp.Regexp
	// vocab records which vocabulary groups are asserted vs. ignored.
	vocab vocabSet
	// dynScope is the stack of resource base URIs currently being evaluated,
	// outermost first. It drives $dynamicRef / $recursiveRef resolution.
	dynScope []string
}

// enterScope pushes base if it differs from the current innermost scope, and
// returns a function that pops it (a no-op if nothing was pushed).
func (v *validator) enterScope(base string) func() {
	if n := len(v.dynScope); n > 0 && v.dynScope[n-1] == base {
		return func() {}
	}
	v.dynScope = append(v.dynScope, base)
	return func() { v.dynScope = v.dynScope[:len(v.dynScope)-1] }
}

// resolveDynamic implements $dynamicRef / $recursiveRef resolution against the
// current dynamic scope.
func (v *validator) resolveDynamic(sch *ir.Schema) (*ir.Schema, error) {
	ref := sch.DynamicRef
	lex, lexErr := v.ldr.Resolve(sch.BaseURI, ref)
	name := fragmentName(ref)

	if name != "" {
		// 2020-12 named $dynamicAnchor: only bounce to the dynamic scope when
		// the lexical target itself declares a matching $dynamicAnchor.
		if lex != nil && lex.DynamicAnchor == name {
			for _, base := range v.dynScope {
				if s, ok := v.ldr.DynamicAnchorInBase(base, name); ok {
					return s, nil
				}
			}
		}
		if lexErr != nil {
			return nil, lexErr
		}
		return lex, nil
	}

	// 2019-09 $recursiveRef ("#"): bounce only when the lexical target is a
	// recursive anchor; otherwise it behaves as a plain $ref.
	if lex != nil && lex.RecursiveAnchor {
		for _, base := range v.dynScope {
			if s, ok := v.ldr.RecursiveAnchorInBase(base); ok {
				return s, nil
			}
		}
	}
	if lexErr != nil {
		return nil, lexErr
	}
	return lex, nil
}

// fragmentName returns the fragment of a reference (the part after '#'),
// percent-decoded, or "" when there is none or it is empty.
func fragmentName(ref string) string {
	i := strings.IndexByte(ref, '#')
	if i < 0 {
		return ""
	}
	frag := ref[i+1:]
	if strings.HasPrefix(frag, "/") {
		return "" // a JSON pointer fragment is not an anchor name
	}
	if dec, err := url.PathUnescape(frag); err == nil {
		return dec
	}
	return frag
}

// result holds the annotations a successful validation contributes: which
// object properties and which array items were evaluated. They feed
// unevaluatedProperties / unevaluatedItems.
type result struct {
	evalProps map[string]bool
	evalItems map[int]bool
}

func (r *result) markProp(name string) {
	if r.evalProps == nil {
		r.evalProps = map[string]bool{}
	}
	r.evalProps[name] = true
}

func (r *result) markItem(i int) {
	if r.evalItems == nil {
		r.evalItems = map[int]bool{}
	}
	r.evalItems[i] = true
}

// merge folds another result's annotations into r.
func (r *result) merge(o result) {
	for k := range o.evalProps {
		r.markProp(k)
	}
	for i := range o.evalItems {
		r.markItem(i)
	}
}

func (v *validator) err(iloc, kloc, keyword, msg string) *xvalid.Error {
	return &xvalid.Error{
		InstanceLocation: iloc,
		KeywordLocation:  kloc,
		Keyword:          keyword,
		Message:          msg,
	}
}

// pattern returns a compiled (and cached) RE2 regexp for p.
func (v *validator) pattern(p string) (*regexp.Regexp, error) {
	if re, ok := v.patterns[p]; ok {
		return re, nil
	}
	re, err := xvalid.CompilePattern(p)
	if err != nil {
		return nil, err
	}
	v.patterns[p] = re
	return re, nil
}

func typeMatches(ts ir.TypeSet, inst any) bool {
	for _, t := range ts {
		if matchesType(t, inst) {
			return true
		}
	}
	return false
}

func matchesType(t ir.Type, inst any) bool {
	switch t {
	case ir.TypeNull:
		return inst == nil
	case ir.TypeBoolean:
		_, ok := inst.(bool)
		return ok
	case ir.TypeObject:
		_, ok := inst.(map[string]any)
		return ok
	case ir.TypeArray:
		_, ok := inst.([]any)
		return ok
	case ir.TypeString:
		_, ok := inst.(string)
		return ok
	case ir.TypeNumber:
		return isNumber(inst)
	case ir.TypeInteger:
		return isInteger(inst)
	}
	return false
}

func isNumber(inst any) bool {
	switch inst.(type) {
	case float64, int, int64, json.Number:
		return true
	}
	return false
}

func isInteger(inst any) bool {
	switch n := inst.(type) {
	case float64:
		return !math.IsInf(n, 0) && !math.IsNaN(n) && n == math.Trunc(n)
	case int, int64:
		return true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			_ = i
			return true
		}
		f, err := n.Float64()
		return err == nil && f == math.Trunc(f)
	}
	return false
}

func numAsRat(inst any) (*big.Rat, bool) {
	switch n := inst.(type) {
	case float64:
		r := new(big.Rat)
		if r.SetFloat64(n) == nil {
			return nil, false
		}
		return r, true
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	case int64:
		return new(big.Rat).SetInt64(n), true
	case json.Number:
		r := new(big.Rat)
		if _, ok := r.SetString(n.String()); ok {
			return r, true
		}
	}
	return nil, false
}

func joinLoc(loc, seg string) string {
	return loc + "/" + escapePtr(seg)
}

func escapePtr(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
