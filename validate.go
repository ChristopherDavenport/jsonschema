package jsonschema

import (
	"fmt"
	"math/big"
	"sort"
	"unicode/utf8"

	"github.com/ChristopherDavenport/jsonschema/ir"
	"github.com/ChristopherDavenport/jsonschema/xvalid"
)

// validate checks inst against sch. On success it returns the annotations the
// schema contributed; on failure it returns an aggregated error and no
// annotations (per spec, only successful schemas contribute annotations).
func (v *validator) validate(sch *ir.Schema, inst any, iloc, kloc string) (result, *xvalid.Error) {
	if sch == nil || sch.AlwaysValid() {
		return result{}, nil
	}
	if sch.AlwaysInvalid() {
		return result{}, v.err(iloc, kloc, "false", "no value is valid here")
	}

	// Track the dynamic scope so $dynamicRef / $recursiveRef can find the
	// outermost matching anchor in the current evaluation path.
	defer v.enterScope(sch.BaseURI)()

	var res result
	var errs []*xvalid.Error
	fail := func(kw, msg string) {
		errs = append(errs, v.err(iloc, kloc+"/"+kw, kw, msg))
	}
	sub := func(kw string, child *ir.Schema, childInst any, childIloc string) (result, bool) {
		r, e := v.validate(child, childInst, childIloc, kloc+"/"+kw)
		if e != nil {
			errs = append(errs, e)
			return result{}, false
		}
		return r, true
	}

	// References.
	if sch.Ref != "" {
		if target, err := v.ldr.Resolve(sch.BaseURI, sch.Ref); err != nil {
			fail("$ref", err.Error())
		} else if r, ok := sub("$ref", target, inst, iloc); ok {
			res.merge(r)
		}
		// draft-07 and earlier: a $ref suppresses all sibling keywords.
		if sch.IgnoreSiblings {
			if len(errs) > 0 {
				agg := v.err(iloc, kloc, "", "value does not satisfy the schema")
				agg.Causes = errs
				return result{}, agg
			}
			return res, nil
		}
	}
	if sch.DynamicRef != "" {
		if target, err := v.resolveDynamic(sch); err != nil {
			fail("$dynamicRef", err.Error())
		} else if r, ok := sub("$dynamicRef", target, inst, iloc); ok {
			res.merge(r)
		}
	}

	// Validation-vocabulary assertions.
	if v.vocab.validation {
		if !sch.Type.Empty() && !typeMatches(sch.Type, inst) {
			fail("type", fmt.Sprintf("value is not of type %s", typeSetString(sch.Type)))
		}
		if sch.Const != nil && !xvalid.JSONEqual(inst, *sch.Const) {
			fail("const", "value does not equal the const")
		}
		if sch.Enum != nil {
			ok := false
			for _, e := range sch.Enum {
				if xvalid.JSONEqual(inst, e) {
					ok = true
					break
				}
			}
			if !ok {
				fail("enum", "value is not one of the enumerated values")
			}
		}
		v.validateNumber(sch, inst, iloc, kloc, fail)
	}

	// validateString/Array/Object internally separate validation-vocabulary
	// assertions (lengths, counts, required) from applicator child keywords.
	v.validateString(sch, inst, iloc, kloc, fail)
	v.validateArray(sch, inst, iloc, kloc, &res, fail, sub)
	v.validateObject(sch, inst, iloc, kloc, &res, fail, sub)

	// Applicator keywords: combinators, conditionals, dependentSchemas.
	if v.vocab.applicator {
		for i, s := range sch.AllOf {
			if r, ok := sub(fmt.Sprintf("allOf/%d", i), s, inst, iloc); ok {
				res.merge(r)
			}
		}
		if len(sch.AnyOf) > 0 {
			anyOK := false
			var branch []*xvalid.Error
			for i, s := range sch.AnyOf {
				r, e := v.validate(s, inst, iloc, fmt.Sprintf("%s/anyOf/%d", kloc, i))
				if e == nil {
					anyOK = true
					res.merge(r)
				} else {
					branch = append(branch, e)
				}
			}
			if !anyOK {
				errs = append(errs, v.err(iloc, kloc+"/anyOf", "anyOf", "value does not match any subschema").Add(branch[0]))
			}
		}
		if len(sch.OneOf) > 0 {
			var passed []result
			for i, s := range sch.OneOf {
				if r, e := v.validate(s, inst, iloc, fmt.Sprintf("%s/oneOf/%d", kloc, i)); e == nil {
					passed = append(passed, r)
				}
			}
			switch len(passed) {
			case 1:
				res.merge(passed[0])
			case 0:
				fail("oneOf", "value does not match any subschema")
			default:
				fail("oneOf", fmt.Sprintf("value matches %d subschemas, expected exactly one", len(passed)))
			}
		}
		if sch.Not != nil {
			if _, e := v.validate(sch.Not, inst, iloc, kloc+"/not"); e == nil {
				fail("not", "value must not match the subschema")
			}
		}

		// Conditionals.
		if sch.If != nil {
			if r, e := v.validate(sch.If, inst, iloc, kloc+"/if"); e == nil {
				res.merge(r)
				if sch.Then != nil {
					if r2, ok := sub("then", sch.Then, inst, iloc); ok {
						res.merge(r2)
					}
				}
			} else if sch.Else != nil {
				if r2, ok := sub("else", sch.Else, inst, iloc); ok {
					res.merge(r2)
				}
			}
		}

		// dependentSchemas (object-only).
		if obj, ok := inst.(map[string]any); ok {
			for name, s := range sch.DependentSchemas {
				if _, present := obj[name]; present {
					if r, ok := sub("dependentSchemas/"+name, s, inst, iloc); ok {
						res.merge(r)
					}
				}
			}
		}
	}

	// Unevaluated keywords run last, consuming the accumulated annotations.
	if v.vocab.unevaluated {
		v.validateUnevaluated(sch, inst, iloc, kloc, &res, fail, sub)
	}

	if len(errs) > 0 {
		agg := v.err(iloc, kloc, "", "value does not satisfy the schema")
		agg.Causes = errs
		return result{}, agg
	}
	return res, nil
}

func (v *validator) validateNumber(sch *ir.Schema, inst any, iloc, kloc string, fail func(string, string)) {
	if sch.MultipleOf == nil && sch.Maximum == nil && sch.ExclusiveMaximum == nil &&
		sch.Minimum == nil && sch.ExclusiveMinimum == nil {
		return
	}
	n, ok := numAsRat(inst)
	if !ok {
		return
	}
	if sch.MultipleOf != nil {
		q := new(big.Rat).Quo(n, sch.MultipleOf)
		if !q.IsInt() {
			fail("multipleOf", "value is not a multiple of "+sch.MultipleOf.RatString())
		}
	}
	if sch.Maximum != nil && n.Cmp(sch.Maximum) > 0 {
		fail("maximum", "value is greater than the maximum")
	}
	if sch.ExclusiveMaximum != nil && n.Cmp(sch.ExclusiveMaximum) >= 0 {
		fail("exclusiveMaximum", "value is greater than or equal to the exclusive maximum")
	}
	if sch.Minimum != nil && n.Cmp(sch.Minimum) < 0 {
		fail("minimum", "value is less than the minimum")
	}
	if sch.ExclusiveMinimum != nil && n.Cmp(sch.ExclusiveMinimum) <= 0 {
		fail("exclusiveMinimum", "value is less than or equal to the exclusive minimum")
	}
}

func (v *validator) validateString(sch *ir.Schema, inst any, iloc, kloc string, fail func(string, string)) {
	s, ok := inst.(string)
	if !ok {
		return
	}
	if v.vocab.validation {
		if sch.MinLength != nil || sch.MaxLength != nil {
			n := uint64(utf8.RuneCountInString(s))
			if sch.MinLength != nil && n < *sch.MinLength {
				fail("minLength", "string is shorter than minLength")
			}
			if sch.MaxLength != nil && n > *sch.MaxLength {
				fail("maxLength", "string is longer than maxLength")
			}
		}
		if sch.Pattern != "" {
			re, err := v.pattern(sch.Pattern)
			if err != nil {
				fail("pattern", "invalid pattern: "+err.Error())
			} else if !re.MatchString(s) {
				fail("pattern", "string does not match pattern")
			}
		}
	}
	if sch.Format != "" && v.assertFormat {
		if valid, _ := xvalid.CheckFormat(sch.Format, s); !valid {
			fail("format", "string is not a valid "+sch.Format)
		}
	}
}

func typeSetString(ts ir.TypeSet) string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.String()
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += " or "
		}
		out += n
	}
	return out
}
