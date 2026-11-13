package models

import (
	"encoding/json"
	"math"
	"strconv"
)

// Number is a JSON number as message templates see it (AXM, LST-22).
//
// A number in subscriber attributes, campaign attributes or transactional data is decoded
// as a float64, and Go templates print a float64 of a million or more with an exponent:
// {{ .Subscriber.Attribs.follower }} came out as 1.234567e+06. Number prints the digits
// instead. It stays a float64 kind, so template comparisons (gt, eq with a float), printf
// with a float verb and the sprig math and conversion functions work as they did.
type Number float64

// String returns the number the way a browser shows a JSON number: plain digits from
// 0.000001 up to (not including) 1e21, and the shortest text that reads back as the
// same value. Outside that range it is the form Go prints by default.
func (n Number) String() string {
	f := float64(n)
	if a := math.Abs(f); a == 0 || (a >= 1e-6 && a < 1e21) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// MarshalJSON writes the number as the float64 it is. Without it, html/template would
// write a value with a String method into a <script> as a quoted string.
func (n Number) MarshalJSON() ([]byte, error) {
	return json.Marshal(float64(n))
}

// PlainNumbers returns m with every float64 in it, at any depth of maps and arrays,
// as a Number. m itself is not changed: the parts that hold a float64 are copied and the
// rest is shared. A map with no float64 comes back as it is.
func PlainNumbers[M ~map[string]any](m M) M {
	out, changed := plainMap(m)
	if !changed {
		return m
	}
	return M(out)
}

func plainMap(m map[string]any) (map[string]any, bool) {
	var out map[string]any
	for k, v := range m {
		nv, changed := plainValue(v)
		if !changed {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(m))
			for k2, v2 := range m {
				out[k2] = v2
			}
		}
		out[k] = nv
	}
	return out, out != nil
}

func plainValue(v any) (any, bool) {
	switch t := v.(type) {
	case float64:
		return Number(t), true
	case map[string]any:
		if out, changed := plainMap(t); changed {
			return out, true
		}
	case JSON:
		if out, changed := plainMap(t); changed {
			return JSON(out), true
		}
	case []any:
		var out []any
		for i, e := range t {
			ne, changed := plainValue(e)
			if !changed {
				continue
			}
			if out == nil {
				out = make([]any, len(t))
				copy(out, t)
			}
			out[i] = ne
		}
		if out != nil {
			return out, true
		}
	}
	return v, false
}
