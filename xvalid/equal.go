package xvalid

import "math/big"

// JSONEqual reports whether two decoded JSON values are equal by JSON
// value-equality: numbers compare by mathematical value (so 1 and 1.0 are
// equal), objects are order-independent, and arrays are order-sensitive.
//
// The inputs are expected to be the shapes produced by encoding/json into an
// any: nil, bool, float64 (or json.Number/int for numbers), string, []any, and
// map[string]any.
func JSONEqual(a, b any) bool {
	// Numeric comparison first, so 1 (int) and 1.0 (float64) unify.
	if ar, ok := toRat(a); ok {
		br, ok := toRat(b)
		return ok && ar.Cmp(br) == 0
	}
	switch av := a.(type) {
	case nil:
		return b == nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !JSONEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, va := range av {
			vb, ok := bv[k]
			if !ok || !JSONEqual(va, vb) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// Unique reports whether every element of items is distinct by JSONEqual.
func Unique(items []any) bool {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if JSONEqual(items[i], items[j]) {
				return false
			}
		}
	}
	return true
}

// toRat converts any JSON numeric representation to an exact rational.
func toRat(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case float64:
		r := new(big.Rat)
		if r.SetFloat64(n) == nil {
			return nil, false // NaN or Inf: not a valid JSON number
		}
		return r, true
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	case int64:
		return new(big.Rat).SetInt64(n), true
	case interface{ String() string }: // json.Number
		r := new(big.Rat)
		if _, ok := r.SetString(n.String()); ok {
			return r, true
		}
		return nil, false
	default:
		return nil, false
	}
}
