package manager

import (
	"fmt"
	"html/template"

	"github.com/knadh/listmonk/models"
)

// numberFuncs replaces the sprig functions that go by the exact type of a value
// (AXM, LST-22). Templates get attribute numbers as models.Number, while a number
// written in a template ({{ has 3.0 .Subscriber.Attribs.scores }}) or returned by
// another function is a float64. To these functions the two would be different values:
// has, without, uniq and deepEqual would not find 3.0 in a list that holds it, and
// typeIs "float64" would say no. They are wrapped so that both are the same number.
//
// A function is replaced only if it still has the signature sprig v3.3 gives it.
// TestCampaignMessageNumberFunctionsUnchanged fails if one of them is left unwrapped, and
// TestTemplateFuncsTakeNumberLikeFloat64 fails if another function tells the two apart.
func numberFuncs(f template.FuncMap) {
	// The list functions and deepEqual see every number as a models.Number.
	plain := models.PlainValue
	plainAll := func(vals []any) []any {
		out, _ := plain(vals).([]any)
		return out
	}

	if fn, ok := f["has"].(func(any, any) bool); ok {
		f["has"] = func(needle, haystack any) bool { return fn(plain(needle), plain(haystack)) }
	}
	if fn, ok := f["mustHas"].(func(any, any) (bool, error)); ok {
		f["mustHas"] = func(needle, haystack any) (bool, error) { return fn(plain(needle), plain(haystack)) }
	}
	if fn, ok := f["without"].(func(any, ...any) []any); ok {
		f["without"] = func(list any, omit ...any) []any { return fn(plain(list), plainAll(omit)...) }
	}
	if fn, ok := f["mustWithout"].(func(any, ...any) ([]any, error)); ok {
		f["mustWithout"] = func(list any, omit ...any) ([]any, error) { return fn(plain(list), plainAll(omit)...) }
	}
	if fn, ok := f["uniq"].(func(any) []any); ok {
		f["uniq"] = func(list any) []any { return fn(plain(list)) }
	}
	if fn, ok := f["mustUniq"].(func(any) ([]any, error)); ok {
		f["mustUniq"] = func(list any) ([]any, error) { return fn(plain(list)) }
	}
	if fn, ok := f["deepEqual"].(func(any, any) bool); ok {
		f["deepEqual"] = func(a, b any) bool { return fn(plain(a), plain(b)) }
	}

	// The type functions name a models.Number as the float64 it is.
	float := func(v any) any {
		if n, ok := v.(models.Number); ok {
			return float64(n)
		}
		return v
	}
	// round takes its optional third value as a float64 and nothing else, so a template
	// would refuse a models.Number there. A number written in the template arrives as an
	// int when it has no decimal point.
	if fn, ok := f["round"].(func(any, int, ...float64) float64); ok {
		f["round"] = func(a any, p int, roundOn ...any) (float64, error) {
			opts := make([]float64, len(roundOn))
			for i, v := range roundOn {
				switch t := float(v).(type) {
				case float64:
					opts[i] = t
				case int:
					opts[i] = float64(t)
				default:
					return 0, fmt.Errorf("wrong type for value; expected float64; got %T", v)
				}
			}
			return fn(a, p, opts...), nil
		}
	}

	if fn, ok := f["typeOf"].(func(any) string); ok {
		f["typeOf"] = func(src any) string { return fn(float(src)) }
	}
	if fn, ok := f["typeIs"].(func(string, any) bool); ok {
		f["typeIs"] = func(target string, src any) bool { return fn(target, float(src)) }
	}
	if fn, ok := f["typeIsLike"].(func(string, any) bool); ok {
		f["typeIsLike"] = func(target string, src any) bool { return fn(target, float(src)) }
	}
}
