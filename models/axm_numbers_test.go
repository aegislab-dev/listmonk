package models

import (
	"encoding/json"
	"html/template"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/volatiletech/null.v6"
)

func TestNumberString(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0, "0"}, {7, "7"}, {14900, "14900"}, {999999, "999999"},
		// From a million on, Go prints a float64 as 1e+06.
		{1000000, "1000000"}, {1234567, "1234567"}, {-1234567, "-1234567"},
		{21474836480, "21474836480"}, {9007199254740991, "9007199254740991"},
		{123456789012345680000, "123456789012345680000"},
		{1.5, "1.5"}, {1234567.25, "1234567.25"}, {0.1, "0.1"}, {0.00001, "0.00001"}, {0.000001, "0.000001"},
		// Outside the range a browser prints in digits, the default form stays.
		{1e21, "1e+21"}, {1.5e300, "1.5e+300"}, {0.0000001, "1e-07"},
	} {
		if got := Number(tc.in).String(); got != tc.want {
			t.Errorf("Number(%v).String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPlainNumbers(t *testing.T) {
	const raw = `{"follower": 1234567, "country": "KR", "vip": true, "none": null, "rate": 0.25,
		"stats": {"posts": 2500000, "name": "x", "deep": {"n": 3000000}},
		"scores": [1000000, "a", {"n": 2000000}, [4000000]], "tags": ["a", "b"], "plain": {"a": "b"}}`
	var in JSON
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(in)

	out := PlainNumbers(in)

	// The input is what it was: other readers of the same map still see float64.
	if after, _ := json.Marshal(in); string(after) != string(before) {
		t.Fatalf("input changed:\n%s\n%s", before, after)
	}
	if _, ok := in["follower"].(float64); !ok {
		t.Fatalf("input follower is %T", in["follower"])
	}
	if _, ok := in["stats"].(map[string]any)["posts"].(float64); !ok {
		t.Fatal("input nested number changed")
	}
	if _, ok := in["scores"].([]any)[0].(float64); !ok {
		t.Fatal("input array number changed")
	}

	// Every number, at any depth, is a Number, and nothing else changed.
	for _, v := range []any{
		out["follower"], out["rate"],
		out["stats"].(map[string]any)["posts"],
		out["stats"].(map[string]any)["deep"].(map[string]any)["n"],
		out["scores"].([]any)[0],
		out["scores"].([]any)[2].(map[string]any)["n"],
		out["scores"].([]any)[3].([]any)[0],
	} {
		if _, ok := v.(Number); !ok {
			t.Errorf("%v is %T, want Number", v, v)
		}
	}
	if out["country"] != "KR" || out["vip"] != true || out["none"] != nil || out["scores"].([]any)[1] != "a" {
		t.Fatalf("other values changed: %v", out)
	}

	// It is written as JSON exactly like the input (postback messenger, API, DB).
	if got, _ := json.Marshal(out); string(got) != string(before) {
		t.Fatalf("JSON differs:\n%s\n%s", before, got)
	}

	// Parts without a number are shared, not copied.
	if reflect.ValueOf(out["plain"]).Pointer() != reflect.ValueOf(in["plain"]).Pointer() ||
		reflect.ValueOf(out["tags"]).Pointer() != reflect.ValueOf(in["tags"]).Pointer() {
		t.Fatal("a part without numbers was copied")
	}

	// A map without numbers, a converted map and an empty or nil map come back as they are.
	for name, m := range map[string]JSON{"no numbers": {"a": "b", "c": []any{"d"}}, "converted": out, "empty": {}, "nil": nil} {
		got := PlainNumbers(m)
		if reflect.ValueOf(got).Pointer() != reflect.ValueOf(m).Pointer() || len(got) != len(m) {
			t.Errorf("%s: a new map came back", name)
		}
	}
	if PlainNumbers(JSON(nil)) != nil {
		t.Fatal("nil became a map")
	}

	// A nested value of the JSON type keeps its type.
	nested := PlainNumbers(map[string]any{"a": JSON{"n": 1500000.0}})
	if _, ok := nested["a"].(JSON)["n"].(Number); !ok {
		t.Fatalf("nested JSON: %T", nested["a"])
	}
}

// What a template does with a number other than print it stays as it was.
func TestNumberInTemplates(t *testing.T) {
	data := map[string]any{"Old": map[string]any{"n": 1234567.0}, "New": PlainNumbers(map[string]any{"n": 1234567.0})}
	for _, tc := range []struct{ expr, old, now string }{
		{`{{ .X.n }}`, "1.234567e&#43;06", "1234567"},
		{`{{ print .X.n }}`, "1.234567e&#43;06", "1234567"},
		{`{{ printf "%v" .X.n }}`, "1.234567e&#43;06", "1234567"},
		{`<a href="https://example.test/?n={{ .X.n }}">x</a>`, `<a href="https://example.test/?n=1.234567e%2b06">x</a>`, `<a href="https://example.test/?n=1234567">x</a>`},
		{`{{ printf "%.0f" .X.n }}`, "1234567", "1234567"},
		{`{{ printf "%.2f" .X.n }}`, "1234567.00", "1234567.00"},
		{`{{ if gt .X.n 1000000.0 }}big{{ else }}small{{ end }}`, "big", "big"},
		{`{{ if lt .X.n 1000000.0 }}small{{ else }}big{{ end }}`, "big", "big"},
		{`{{ if eq .X.n 1234567.0 }}same{{ end }}`, "same", "same"},
		{`{{ if .X.n }}set{{ end }}`, "set", "set"},
		{`<script>var n = {{ .X.n }};</script>`, "<script>var n =  1234567 ;</script>", "<script>var n =  1234567 ;</script>"},
	} {
		for name, want := range map[string]string{"Old": tc.old, "New": tc.now} {
			tpl, err := template.New("t").Parse(strings.ReplaceAll(tc.expr, ".X.", "."+name+"."))
			if err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			if err := tpl.Execute(&b, data); err != nil {
				t.Errorf("%s (%s): %v", tc.expr, name, err)
				continue
			}
			if b.String() != want {
				t.Errorf("%s (%s) = %q, want %q", tc.expr, name, b.String(), want)
			}
		}
	}

	// Comparing with an integer literal failed before and still does.
	for name := range data {
		tpl := template.Must(template.New("t").Parse(`{{ if gt .` + name + `.n 1000000 }}big{{ end }}`))
		if err := tpl.Execute(&strings.Builder{}, data); err == nil || !strings.Contains(err.Error(), "incompatible types") {
			t.Errorf("%s: comparing with an integer: %v", name, err)
		}
	}
}

func TestCampaignCompileTemplatePlainNumbers(t *testing.T) {
	var attribs JSON
	if err := json.Unmarshal([]byte(`{"price": 1500000, "deal": {"count": 2000000}}`), &attribs); err != nil {
		t.Fatal(err)
	}
	c := Campaign{
		Subject: "{{ .Campaign.Attribs.price }}", Body: `<p>{{ .Campaign.Attribs.price }} / {{ .Campaign.Attribs.deal.count }}</p>`,
		ContentType: CampaignContentTypeHTML, Attribs: attribs,
		AltBody: null.StringFrom("{{ .Campaign.Attribs.price }}"),
	}
	if err := c.CompileTemplate(template.FuncMap{}); err != nil {
		t.Fatal(err)
	}
	data := struct{ Campaign *Campaign }{&c}

	var body, subject, alt strings.Builder
	if err := c.Tpl.ExecuteTemplate(&body, BaseTpl, data); err != nil {
		t.Fatal(err)
	}
	if err := c.SubjectTpl.ExecuteTemplate(&subject, ContentTpl, data); err != nil {
		t.Fatal(err)
	}
	if err := c.AltBodyTpl.ExecuteTemplate(&alt, ContentTpl, data); err != nil {
		t.Fatal(err)
	}
	if body.String() != "<p>1500000 / 2000000</p>" || subject.String() != "1500000" || alt.String() != "1500000" {
		t.Fatalf("body=%q subject=%q alt=%q", body.String(), subject.String(), alt.String())
	}

	// The campaign is stored and returned with the same JSON.
	if got, _ := json.Marshal(c.Attribs); string(got) != `{"deal":{"count":2000000},"price":1500000}` {
		t.Fatalf("attribs JSON: %s", got)
	}
	if v, err := c.Attribs.Value(); err != nil || string(v.([]byte)) != `{"deal":{"count":2000000},"price":1500000}` {
		t.Fatalf("attribs DB value: %s %v", v, err)
	}
}

func TestTxMessageRenderPlainNumbers(t *testing.T) {
	var sub Subscriber
	if err := json.Unmarshal([]byte(`{"email": "a@example.invalid", "attribs": {"follower": 1234567, "stats": {"posts": 2500000}}}`), &sub); err != nil {
		t.Fatal(err)
	}
	var m TxMessage
	if err := json.Unmarshal([]byte(`{"data": {"amount": 1500000, "items": [{"price": 2000000}]},
		"altbody": "{{ .Subscriber.Attribs.follower }} {{ .Tx.Data.amount }}"}`), &m); err != nil {
		t.Fatal(err)
	}
	tpl := Template{
		Subject: "{{ .Subscriber.Attribs.follower }} / {{ .Tx.Data.amount }}",
		Body: `<p>{{ .Subscriber.Attribs.follower }} {{ .Subscriber.Attribs.stats.posts }} ` +
			`{{ .Tx.Data.amount }}{{ range .Tx.Data.items }} {{ .price }}{{ end }}</p>`,
	}
	if err := tpl.Compile(template.FuncMap{}); err != nil {
		t.Fatal(err)
	}

	// The same message is rendered once per recipient.
	for i := 0; i < 2; i++ {
		m.AltBody = "{{ .Subscriber.Attribs.follower }} {{ .Tx.Data.amount }}"
		if err := m.Render(sub, &tpl, nil); err != nil {
			t.Fatal(err)
		}
		if string(m.Body) != "<p>1234567 2500000 1500000 2000000</p>" || m.Subject != "1234567 / 1500000" || m.AltBody != "1234567 1500000" {
			t.Fatalf("round %d: body=%q subject=%q alt=%q", i, m.Body, m.Subject, m.AltBody)
		}
	}

	// The caller's subscriber is not touched.
	if _, ok := sub.Attribs["follower"].(float64); !ok {
		t.Fatalf("caller's attribs changed: %T", sub.Attribs["follower"])
	}
}
