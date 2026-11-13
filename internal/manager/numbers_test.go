package manager

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

// A subscriber as the database gives it: attribute numbers are float64.
func numbersSubscriber(t *testing.T, attribs string) models.Subscriber {
	t.Helper()
	sub := models.Subscriber{UUID: "subscriber", Email: "a@example.invalid", Name: "Alice", Attribs: models.JSON{}}
	if err := sub.Attribs.Scan([]byte(attribs)); err != nil {
		t.Fatal(err)
	}
	return sub
}

func numbersManager() *Manager {
	return New(Config{
		RootURL:      "https://lists.example.test",
		LinkTrackURL: "https://lists.example.test/link/%s/%s/%s",
		ViewTrackURL: "https://lists.example.test/campaign/%s/%s/px.png",
		UnsubURL:     "https://lists.example.test/subscription/%s/%s",
		MessageURL:   "https://lists.example.test/campaign/%s/%s",
		OptinURL:     "https://lists.example.test/subscription/optin/%s?%s",
	}, &trackingStore{}, nil, log.New(io.Discard, "", 0))
}

// LST-22: {{ .Subscriber.Attribs.follower }} printed 1.234567e+06 for a million or more.
func TestCampaignMessagePrintsAttributeNumbersAsDigits(t *testing.T) {
	m := numbersManager()
	for _, contentType := range []string{models.CampaignContentTypeHTML, models.CampaignContentTypeRichtext,
		models.CampaignContentTypeVisual, models.CampaignContentTypeMarkdown, models.CampaignContentTypePlain} {
		t.Run(contentType, func(t *testing.T) {
			c := models.Campaign{
				UUID: "campaign", ContentType: contentType, Messenger: "postback",
				Subject: "{{ .Subscriber.Attribs.follower }} followers",
				Body:    `{{ .Subscriber.Attribs.follower }} / {{ .Subscriber.Attribs.small }} / {{ .Subscriber.Attribs.text }} / {{ .Subscriber.Attribs.stats.posts }} / {{ index .Subscriber.Attribs "팔로워" }} / {{ .Subscriber.Attribs.missing | default "none" }}`,
				AltBody: null.StringFrom("{{ .Subscriber.Attribs.follower }} alt"),
				Headers: models.Headers{{"X-Followers": "{{ .Subscriber.Attribs.follower }}"}},
			}
			if err := c.CompileTemplate(m.TemplateFuncs(&c)); err != nil {
				t.Fatal(err)
			}
			sub := numbersSubscriber(t, `{"follower": 1234567, "small": 14900, "text": "1,234,567", "stats": {"posts": 2500000}, "팔로워": 21474836480}`)
			msg, err := m.NewCampaignMessage(&c, sub)
			if err != nil {
				t.Fatal(err)
			}

			body := string(msg.Body())
			if contentType == models.CampaignContentTypeMarkdown {
				body = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(body), "<p>"), "</p>")
			}
			if want := "1234567 / 14900 / 1,234,567 / 2500000 / 21474836480 / none"; body != want {
				t.Errorf("body = %q, want %q", body, want)
			}
			if msg.Subject() != "1234567 followers" {
				t.Errorf("subject = %q", msg.Subject())
			}
			if contentType != models.CampaignContentTypePlain && string(msg.AltBody()) != "1234567 alt" {
				t.Errorf("alt body = %q", msg.AltBody())
			}
			if got := msg.headers[0]["X-Followers"]; got != "1234567" {
				t.Errorf("header = %q", got)
			}

			// The caller's subscriber keeps its float64, and the message's subscriber is
			// written as the same JSON (the postback messenger sends it).
			if _, ok := sub.Attribs["follower"].(float64); !ok {
				t.Fatalf("caller's attribs changed: %T", sub.Attribs["follower"])
			}
			want, _ := json.Marshal(sub.Attribs)
			if got, _ := json.Marshal(msg.Subscriber.Attribs); string(got) != string(want) {
				t.Fatalf("attribs JSON differs:\n%s\n%s", want, got)
			}
		})
	}
}

// Templates that already work around the float (sprig math, conversions, float
// comparisons, printf) give what they gave before.
func TestCampaignMessageNumberFunctionsUnchanged(t *testing.T) {
	m := numbersManager()
	for _, tc := range []struct{ expr, want string }{
		{`{{ int .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ int64 .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ .Subscriber.Attribs.follower | int }}`, "1234567"},
		{`{{ printf "%.0f" .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ printf "%d" (int .Subscriber.Attribs.follower) }}`, "1234567"},
		{`{{ add .Subscriber.Attribs.follower 1 }}`, "1234568"},
		{`{{ mul .Subscriber.Attribs.follower 2 }}`, "2469134"},
		{`{{ div .Subscriber.Attribs.follower 10000 }}`, "123"},
		{`{{ mod .Subscriber.Attribs.follower 10 }}`, "7"},
		{`{{ max .Subscriber.Attribs.follower 5 }}`, "1234567"},
		{`{{ divf .Subscriber.Attribs.follower 2.0 }}`, "617283.5"},
		{`{{ if gt .Subscriber.Attribs.follower 1000000.0 }}big{{ else }}small{{ end }}`, "big"},
		{`{{ if gt (int .Subscriber.Attribs.follower) 1000000 }}big{{ else }}small{{ end }}`, "big"},
		{`{{ if ge .Subscriber.Attribs.small 1000000.0 }}big{{ else }}small{{ end }}`, "small"},
		{`{{ if eq .Subscriber.Attribs.follower 1234567.0 }}same{{ end }}`, "same"},
		{`{{ if .Subscriber.Attribs.zero }}set{{ else }}unset{{ end }}`, "unset"},
		{`{{ .Subscriber.Attribs.zero | default "none" }}`, "none"},
		{`{{ .Subscriber.Attribs.follower | default "none" }}`, "1234567"},
		{`{{ kindOf .Subscriber.Attribs.follower }}`, "float64"},
		{`{{ toJson .Subscriber.Attribs.follower }}`, "1234567"},
		// sprig functions that go by the exact type: a number written in the template is
		// a float64, the attribute is a models.Number (numberFuncs).
		{`{{ has 3.0 .Subscriber.Attribs.nums }}`, "true"},
		{`{{ has 1000000.0 .Subscriber.Attribs.nums }}`, "true"},
		{`{{ has 4.0 .Subscriber.Attribs.nums }}`, "false"},
		{`{{ has .Subscriber.Attribs.follower (list 1234567.0 2.0) }}`, "true"},
		{`{{ has .Subscriber.Attribs.three .Subscriber.Attribs.nums }}`, "true"},
		{`{{ has .Subscriber.Attribs.small .Subscriber.Attribs.nums }}`, "false"},
		{`{{ has "a" (list "a" "b") }}{{ has 3 (list 1 2 3) }}{{ has 3 .Subscriber.Attribs.nums }}`, "truetruefalse"},
		{`{{ mustHas 3.0 .Subscriber.Attribs.nums }}`, "true"},
		{`{{ without .Subscriber.Attribs.nums 3.0 }}`, "[1000000 2000000]"},
		{`{{ without .Subscriber.Attribs.nums }}`, "[1000000 2000000 3]"},
		{`{{ mustWithout .Subscriber.Attribs.nums 3.0 1000000.0 }}`, "[2000000]"},
		{`{{ len (uniq (list .Subscriber.Attribs.follower 1234567.0 2.0)) }}`, "2"},
		{`{{ len (mustUniq (list .Subscriber.Attribs.three 3.0 3.0)) }}`, "1"},
		{`{{ deepEqual .Subscriber.Attribs.follower 1234567.0 }}`, "true"},
		{`{{ deepEqual .Subscriber.Attribs.nums (list 1000000.0 2000000.0 3.0) }}`, "true"},
		{`{{ deepEqual .Subscriber.Attribs.follower 1234567 }}{{ deepEqual .Subscriber.Attribs.follower 5.0 }}`, "falsefalse"},
		{`{{ typeIs "float64" .Subscriber.Attribs.follower }}`, "true"},
		{`{{ typeIsLike "float64" .Subscriber.Attribs.follower }}`, "true"},
		{`{{ typeOf .Subscriber.Attribs.follower }}`, "float64"},
		{`{{ typeOf .Subscriber.Attribs.nums }} {{ typeOf "a" }} {{ typeIs "string" "a" }}`, "[]interface {} string true"},
		{`{{ round 2.5 0 .Subscriber.Attribs.half }} {{ round 2.5 0 0.5 }} {{ round 2.4 0 }} {{ round 2.5 0 1 }}`, "3 3 2 2"},
		// These printed the exponent as well.
		{`{{ toString .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ printf "%v" .Subscriber.Attribs.follower }}`, "1234567"},
		{`{{ cat .Subscriber.Attribs.follower "followers" }}`, "1234567 followers"},
		{`{{ atoi (toString .Subscriber.Attribs.follower) }}`, "1234567"},
	} {
		c := models.Campaign{UUID: "campaign", ContentType: models.CampaignContentTypePlain, Messenger: "postback", Body: tc.expr}
		if err := c.CompileTemplate(m.TemplateFuncs(&c)); err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		msg, err := m.NewCampaignMessage(&c, numbersSubscriber(t, `{"follower": 1234567, "small": 14900, "zero": 0, "three": 3, "half": 0.5, "nums": [1000000, 2000000, 3]}`))
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got := string(msg.Body()); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// A transactional template gets the same functions.
func TestTxMessageNumberFunctions(t *testing.T) {
	m := numbersManager()
	tpl := models.Template{Body: `{{ has 1500000.0 (list .Tx.Data.amount) }} {{ typeIs "float64" .Subscriber.Attribs.follower }} {{ deepEqual .Tx.Data.amount 1500000.0 }}`}
	if err := tpl.Compile(m.GenericTemplateFuncs()); err != nil {
		t.Fatal(err)
	}
	var msg models.TxMessage
	if err := json.Unmarshal([]byte(`{"data": {"amount": 1500000}}`), &msg); err != nil {
		t.Fatal(err)
	}
	if err := msg.Render(numbersSubscriber(t, `{"follower": 1234567}`), &tpl, m.GenericTemplateFuncs()); err != nil {
		t.Fatal(err)
	}
	if string(msg.Body) != "true true true" {
		t.Fatalf("body = %q", msg.Body)
	}
}

// Every template function is called with the same arguments twice: once with attribute
// values as float64 (what templates got before LST-22) and once as models.Number, next to
// numbers written in the template (float64 both times). The results must be the same
// values. Only the functions that turn a number into text may differ, in notation.
// A sprig upgrade that adds a function which tells the two types apart fails here.
func TestTemplateFuncsTakeNumberLikeFloat64(t *testing.T) {
	funcs := numbersManager().GenericTemplateFuncs()

	// Text of a number: 1.234567e+06 before, 1234567 now. dict makes its keys the same way
	// and toDecimal reads that text.
	notation := map[string]bool{"toString": true, "toStrings": true, "quote": true, "squote": true, "cat": true,
		"join": true, "sortAlpha": true, "dict": true, "toDecimal": true}
	// Not repeatable (clock, random) or slow (keys, password hashes).
	skip := map[string]bool{}
	for _, n := range strings.Fields(`ago date dateInZone date_in_zone dateModify date_modify mustDateModify must_date_modify
		htmlDate htmlDateInZone toDate mustToDate randAlphaNum randAlpha randAscii randNumeric randInt randBytes shuffle
		genPrivateKey genCA genCAWithKey genSelfSignedCert genSelfSignedCertWithKey genSignedCert genSignedCertWithKey
		buildCustomCert derivePassword htpasswd bcrypt encryptAES decryptAES`) {
		skip[n] = true
	}

	// attr values are attribute values; the others stand for what a template writes.
	type arg struct {
		name string
		v    any
		attr bool
		// now, when set, is the value in the second run (v is the value before LST-22).
		now any
	}
	// Built anew for every call: set, unset and merge change their arguments.
	pool := func() []arg {
		return []arg{
			{"attr 1234567", 1234567.0, true, nil}, {"attr 3", 3.0, true, nil},
			{"attr [1000000 2000000 3]", []any{1000000.0, 2000000.0, 3.0}, true, nil},
			{"attr {posts: 2500000}", map[string]any{"posts": 2500000.0}, true, nil},
			// A list made in the template of an attribute and a written number.
			{"[attr 3, 3.0]", []any{3.0, 3.0}, true, []any{models.Number(3), 3.0}},
			{"3.0", 3.0, false, nil}, {"1234567.0", 1234567.0, false, nil}, {"[3.0 1234567.0]", []any{3.0, 1234567.0}, false, nil},
			{`"posts"`, "posts", false, nil}, {`"float64"`, "float64", false, nil}, {"3", 3, false, nil},
		}
	}
	size := len(pool())

	call := func(fn reflect.Value, args []reflect.Value) (out string) {
		defer func() {
			if r := recover(); r != nil {
				out = "panic"
			}
		}()
		var parts []string
		for _, r := range fn.Call(args) {
			if e, ok := r.Interface().(error); ok && e != nil {
				return "error"
			}
			// Compare values, not types: both results with every number as a Number.
			parts = append(parts, fmt.Sprintf("%#v", models.PlainValue(r.Interface())))
		}
		return strings.Join(parts, " / ")
	}

	var names []string
	for n := range funcs {
		if !skip[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	calls := 0
	for _, name := range names {
		fn := reflect.ValueOf(funcs[name])
		ft := fn.Type()
		fixed, counts := ft.NumIn(), []int{ft.NumIn()}
		if ft.IsVariadic() {
			// No, one and two values for the variadic parameter.
			fixed--
			counts = []int{fixed, fixed + 1, fixed + 2}
		}
		for _, n := range counts {
			if n == 0 || n > 4 {
				continue
			}
			idx := make([]int, n)
			for {
				var (
					a, b    []reflect.Value
					desc    []string
					fits    = true
					hasAttr bool
				)
				for i, pi := range idx {
					pt := ft.In(min(i, ft.NumIn()-1))
					if i >= fixed {
						pt = pt.Elem()
					}
					p := pool()[pi]
					va, vb := reflect.ValueOf(p.v), reflect.ValueOf(pool()[pi].v)
					if p.now != nil {
						vb, hasAttr = reflect.ValueOf(p.now), true
					} else if p.attr {
						vb, hasAttr = reflect.ValueOf(models.PlainValue(vb.Interface())), true
					}
					// A template refuses a value that the parameter's type does not take.
					if va.Type().AssignableTo(pt) != vb.Type().AssignableTo(pt) {
						t.Errorf("%s: argument %d takes %s as only one of float64 and Number", name, i+1, p.name)
					}
					if !va.Type().AssignableTo(pt) || !vb.Type().AssignableTo(pt) {
						fits = false
						break
					}
					a, b, desc = append(a, va), append(b, vb), append(desc, p.name)
				}
				if fits && hasAttr {
					calls++
					if before, now := call(fn, a), call(fn, b); before != now && !notation[name] {
						t.Errorf("%s %s\n    float64: %s\n    Number:  %s", name, strings.Join(desc, " "), before, now)
					}
				}

				i := 0
				for ; i < n; i++ {
					if idx[i]++; idx[i] < size {
						break
					}
					idx[i] = 0
				}
				if i == n {
					break
				}
			}
		}
	}
	if calls < 10000 {
		t.Fatalf("only %d calls were compared", calls)
	}
}
