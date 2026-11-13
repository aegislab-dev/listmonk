package models

import (
	"encoding/json"
	"math"
	"reflect"
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
	var p plainer
	out, changed := p.mapOf(m)
	if !changed {
		return m
	}
	return M(out)
}

// PlainValue is PlainNumbers for a value of any kind: a float64, or a map or an array
// that holds one, comes back with Number in its place. Anything else comes back as it is.
func PlainValue(v any) any {
	var p plainer
	out, _ := p.value(v)
	return out
}

// plainer walks one value. It remembers the maps and arrays that hold other maps or
// arrays, so that a value which contains itself (a template can build one with sprig's
// set) ends the walk instead of the process, and a part that is reachable twice is
// walked once. Attributes read from JSON hold neither; a flat map costs nothing extra.
type plainer struct {
	seen map[plainKey]plainResult
}

// plainKey names a map (n is -1) or an array (n is its length) by its address.
type plainKey struct {
	p uintptr
	n int
}

type plainResult struct {
	v       any
	changed bool
}

func (p *plainer) mapOf(m map[string]any) (map[string]any, bool) {
	var (
		out    map[string]any
		key    plainKey
		marked bool
	)
	for k, v := range m {
		// Before going into a map or an array inside m, note that m is being walked:
		// if it turns up again further down, it is left as it is there.
		if !marked && isContainer(v) {
			key, marked = plainKey{reflect.ValueOf(m).Pointer(), -1}, true
			p.note(key, m, false)
		}
		nv, changed := p.value(v)
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
	if marked && out != nil {
		p.note(key, out, true)
	}
	return out, out != nil
}

func (p *plainer) sliceOf(s []any) ([]any, bool) {
	var (
		out    []any
		key    plainKey
		marked bool
	)
	for i, v := range s {
		if !marked && isContainer(v) {
			key, marked = plainKey{reflect.ValueOf(s).Pointer(), len(s)}, true
			p.note(key, s, false)
		}
		nv, changed := p.value(v)
		if !changed {
			continue
		}
		if out == nil {
			out = make([]any, len(s))
			copy(out, s)
		}
		out[i] = nv
	}
	if marked && out != nil {
		p.note(key, out, true)
	}
	return out, out != nil
}

func (p *plainer) value(v any) (any, bool) {
	switch t := v.(type) {
	case float64:
		return Number(t), true
	case map[string]any:
		if r, ok := p.seen[plainKey{reflect.ValueOf(t).Pointer(), -1}]; ok {
			return r.v, r.changed
		}
		if out, changed := p.mapOf(t); changed {
			return out, true
		}
	case JSON:
		if r, ok := p.seen[plainKey{reflect.ValueOf(t).Pointer(), -1}]; ok {
			if m, ok := r.v.(map[string]any); ok {
				return JSON(m), r.changed
			}
			return r.v, r.changed
		}
		if out, changed := p.mapOf(t); changed {
			return JSON(out), true
		}
	case []any:
		if len(t) == 0 {
			return v, false
		}
		if r, ok := p.seen[plainKey{reflect.ValueOf(t).Pointer(), len(t)}]; ok {
			return r.v, r.changed
		}
		if out, changed := p.sliceOf(t); changed {
			return out, true
		}
	}
	return v, false
}

func (p *plainer) note(key plainKey, v any, changed bool) {
	if p.seen == nil {
		p.seen = map[plainKey]plainResult{}
	}
	p.seen[key] = plainResult{v, changed}
}

func isContainer(v any) bool {
	switch v.(type) {
	case map[string]any, JSON, []any:
		return true
	}
	return false
}
