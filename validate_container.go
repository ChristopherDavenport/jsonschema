package jsonschema

import (
	"fmt"
	"strconv"

	"github.com/ChristopherDavenport/jsonschema/ir"
	"github.com/ChristopherDavenport/jsonschema/xvalid"
)

// subFn validates a child schema, recording any error, and returns its
// annotations and whether it passed.
type subFn = func(kw string, child *ir.Schema, childInst any, childIloc string) (result, bool)

func (v *validator) validateArray(sch *ir.Schema, inst any, iloc, kloc string, res *result, fail func(string, string), sub subFn) {
	arr, ok := inst.([]any)
	if !ok {
		return
	}
	n := len(arr)

	if v.vocab.validation {
		if sch.MinItems != nil && uint64(n) < *sch.MinItems {
			fail("minItems", "array has too few items")
		}
		if sch.MaxItems != nil && uint64(n) > *sch.MaxItems {
			fail("maxItems", "array has too many items")
		}
		if sch.UniqueItems && !xvalid.Unique(arr) {
			fail("uniqueItems", "array items are not unique")
		}
	}

	if !v.vocab.applicator {
		return
	}

	// prefixItems: positional tuple validation.
	for i, s := range sch.PrefixItems {
		if i >= n {
			break
		}
		sub(fmt.Sprintf("prefixItems/%d", i), s, arr[i], joinLoc(iloc, strconv.Itoa(i)))
		res.markItem(i)
	}

	// items: applies to every index at or beyond the prefix.
	if sch.Items != nil {
		for i := len(sch.PrefixItems); i < n; i++ {
			sub("items", sch.Items, arr[i], joinLoc(iloc, strconv.Itoa(i)))
			res.markItem(i)
		}
	}

	// contains / minContains / maxContains.
	if sch.Contains != nil {
		matches := 0
		for i := 0; i < n; i++ {
			if _, e := v.validate(sch.Contains, arr[i], joinLoc(iloc, strconv.Itoa(i)), kloc+"/contains"); e == nil {
				matches++
				res.markItem(i)
			}
		}
		min := uint64(1)
		if sch.MinContains != nil {
			min = *sch.MinContains
		}
		if uint64(matches) < min {
			if sch.MinContains != nil {
				fail("minContains", "too few items match contains")
			} else {
				fail("contains", "no items match contains")
			}
		}
		if sch.MaxContains != nil && uint64(matches) > *sch.MaxContains {
			fail("maxContains", "too many items match contains")
		}
	}
}

func (v *validator) validateObject(sch *ir.Schema, inst any, iloc, kloc string, res *result, fail func(string, string), sub subFn) {
	obj, ok := inst.(map[string]any)
	if !ok {
		return
	}

	if v.vocab.validation {
		if sch.MinProperties != nil && uint64(len(obj)) < *sch.MinProperties {
			fail("minProperties", "object has too few properties")
		}
		if sch.MaxProperties != nil && uint64(len(obj)) > *sch.MaxProperties {
			fail("maxProperties", "object has too many properties")
		}
		for _, name := range sch.Required {
			if _, present := obj[name]; !present {
				fail("required", "missing required property "+strconv.Quote(name))
			}
		}
		for name, deps := range sch.DependentRequired {
			if _, present := obj[name]; !present {
				continue
			}
			for _, dep := range deps {
				if _, ok := obj[dep]; !ok {
					fail("dependentRequired", fmt.Sprintf("property %s requires %s", strconv.Quote(name), strconv.Quote(dep)))
				}
			}
		}
	}

	if !v.vocab.applicator {
		return
	}

	matched := make(map[string]bool, len(obj))

	for name, s := range sch.Properties {
		if val, present := obj[name]; present {
			sub("properties/"+name, s, val, joinLoc(iloc, name))
			res.markProp(name)
			matched[name] = true
		}
	}

	for pat, s := range sch.PatternProperties {
		re, err := v.pattern(pat)
		if err != nil {
			fail("patternProperties", "invalid pattern "+strconv.Quote(pat))
			continue
		}
		for name, val := range obj {
			if re.MatchString(name) {
				sub("patternProperties/"+pat, s, val, joinLoc(iloc, name))
				res.markProp(name)
				matched[name] = true
			}
		}
	}

	if sch.AdditionalProperties != nil {
		for name, val := range obj {
			if !matched[name] {
				sub("additionalProperties", sch.AdditionalProperties, val, joinLoc(iloc, name))
				res.markProp(name)
			}
		}
	}

	if sch.PropertyNames != nil {
		for name := range obj {
			sub("propertyNames", sch.PropertyNames, name, joinLoc(iloc, name))
		}
	}
}

func (v *validator) validateUnevaluated(sch *ir.Schema, inst any, iloc, kloc string, res *result, fail func(string, string), sub subFn) {
	if sch.UnevaluatedProperties != nil {
		if obj, ok := inst.(map[string]any); ok {
			for name, val := range obj {
				if !res.evalProps[name] {
					sub("unevaluatedProperties", sch.UnevaluatedProperties, val, joinLoc(iloc, name))
					res.markProp(name)
				}
			}
		}
	}
	if sch.UnevaluatedItems != nil {
		if arr, ok := inst.([]any); ok {
			for i := 0; i < len(arr); i++ {
				if !res.evalItems[i] {
					sub("unevaluatedItems", sch.UnevaluatedItems, arr[i], joinLoc(iloc, strconv.Itoa(i)))
					res.markItem(i)
				}
			}
		}
	}
}
