package manager

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/knadh/listmonk/models"
	"gopkg.in/volatiletech/null.v6"
)

var testFooterLabels = BusinessFooterLabels{
	Representative: "대표", RegistrationNo: "사업자등록번호", Phone: "전화", Email: "이메일",
}

func TestBuildBusinessFooter(t *testing.T) {
	info := models.BusinessFooter{
		Enabled: true, CompanyName: `Example <script> & "Company"`, Representative: "Sample",
		RegistrationNo: "000-00-00000", Address: "Example address", Phone: "000-0000-0000",
		Email: "sample@example.invalid", Extra: "First <b>line</b>\r\nSecond & line\rThird",
	}
	markup, plain := BuildBusinessFooter(info, testFooterLabels)
	for _, want := range []string{`Example &lt;script&gt; &amp; &#34;Company&#34;`, "대표 Sample · 사업자등록번호 000-00-00000", "First &lt;b&gt;line&lt;/b&gt;<br>Second &amp; line<br>Third"} {
		if !bytes.Contains(markup, []byte(want)) {
			t.Errorf("HTML missing %q: %s", want, markup)
		}
	}
	if bytes.Contains(markup, []byte("<script>")) {
		t.Fatal("Unescaped HTML")
	}
	if !bytes.Contains(plain, []byte(info.CompanyName)) || !bytes.HasPrefix(plain, []byte("--\n")) || !bytes.HasSuffix(plain, []byte("First <b>line</b>\nSecond & line\nThird")) {
		t.Fatalf("Unexpected text footer: %s", plain)
	}

	// Omit optional fields and their separators, labels and empty paragraphs.
	info = models.BusinessFooter{Enabled: true, CompanyName: "Example"}
	markup, plain = BuildBusinessFooter(info, testFooterLabels)
	if string(plain) != "--\nExample" || bytes.Count(markup, []byte("<p ")) != 1 || bytes.Contains(markup, []byte(" · ")) {
		t.Fatalf("Empty fields not omitted: %s / %s", markup, plain)
	}
	info.Enabled = false
	markup, plain = BuildBusinessFooter(info, testFooterLabels)
	if len(markup) != 0 || len(plain) != 0 {
		t.Fatal("Disabled footer must be empty")
	}
}

func TestInsertBusinessFooter(t *testing.T) {
	footer := []byte("FOOTER")
	for _, tc := range []struct{ name, body, want string }{
		{"HTML", "<body>unsubscribe</body></html>", "<body>unsubscribeFOOTER</body></html>"},
		{"uppercase", "<BODY>unsubscribe</BoDy></HTML>", "<BODY>unsubscribeFOOTER</BoDy></HTML>"},
		{"space before body end", "<body>unsubscribe</body >\n</html >", "<body>unsubscribeFOOTER</body >\n</html >"},
		{"HTML whitespace before body end", "<body>unsubscribe</BoDy \t\r\n\f></html>", "<body>unsubscribeFOOTER</BoDy \t\r\n\f></html>"},
		{"fragment", "<p>unsubscribe</p>", "<p>unsubscribe</p>FOOTER"},
		{"last closing tag", "<body>one</body>two</body>", "<body>one</body>twoFOOTER</body>"},
		{"Unicode", "<body>한국어 Ⱥ</body>", "<body>한국어 ȺFOOTER</body>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			got := insertHTMLFooter(body, footer, "")
			if string(got) != tc.want || string(body) != tc.body {
				t.Fatalf("Unexpected result or mutated input: %s", got)
			}
		})
	}
	body := []byte("unsubscribe 000-00-00000")
	for _, insert := range []func([]byte, []byte, string) []byte{insertHTMLFooter, appendTextFooter} {
		for _, tc := range []struct {
			footer []byte
			marker string
		}{
			{nil, ""}, {footer, "000-00-00000"},
		} {
			out := insert(body, tc.footer, tc.marker)
			if !bytes.Equal(body, out) || &body[0] != &out[0] {
				t.Fatal("Disabled or duplicate body should be returned unchanged")
			}
		}
		if bytes.Equal(insert(body, footer, ""), body) {
			t.Fatal("Empty registration number must not suppress insertion")
		}
	}
	if got := string(appendTextFooter([]byte("unsubscribe"), []byte("--\nExample"), "")); got != "unsubscribe\n\n--\nExample" {
		t.Fatal(got)
	}
}

func TestCampaignBusinessFooter(t *testing.T) {
	markup, plain := BuildBusinessFooter(models.BusinessFooter{Enabled: true, CompanyName: "Example", RegistrationNo: "000-00-00000"}, testFooterLabels)
	enabled := Manager{cfg: Config{BusinessFooterHTML: markup, BusinessFooterText: plain, BusinessFooterMarker: "000-00-00000"}}
	disabled := Manager{}
	for _, format := range []string{models.CampaignContentTypeHTML, models.CampaignContentTypeRichtext, models.CampaignContentTypeMarkdown, models.CampaignContentTypeVisual, models.CampaignContentTypePlain} {
		for _, messenger := range []string{"email", "email-example", "sms", "emailish", ""} {
			t.Run(format+"/"+messenger, func(t *testing.T) {
				c := models.Campaign{ContentType: format, Messenger: messenger, Body: "Content", TemplateBody: `<body>{{ template "content" . }}<a href="unsubscribe">unsubscribe</a></body>`, AltBody: null.StringFrom("Alternative content"), Headers: models.Headers{{"X-Example": "value"}}}
				if format == models.CampaignContentTypePlain {
					c.TemplateBody = ""
					c.AltBody = null.String{}
				}
				if format == models.CampaignContentTypeVisual {
					c.Body = `<body>Visual content<a href="unsubscribe">unsubscribe</a></body>`
				}
				if err := c.CompileTemplate(nil); err != nil {
					t.Fatal(err)
				}
				original, err := disabled.NewCampaignMessage(&c, models.Subscriber{})
				if err != nil {
					t.Fatal(err)
				}
				msg, err := enabled.NewCampaignMessage(&c, models.Subscriber{})
				if err != nil {
					t.Fatal(err)
				}
				if messenger != "email" && messenger != "email-example" {
					if !bytes.Equal(original.Body(), msg.Body()) || !bytes.Equal(original.AltBody(), msg.AltBody()) {
						t.Fatal("Non-email changed")
					}
					return
				}
				expected := insertHTMLFooter(original.Body(), markup, "000-00-00000")
				if format == models.CampaignContentTypePlain {
					expected = appendTextFooter(original.Body(), plain, "000-00-00000")
				}
				if !bytes.Equal(msg.Body(), expected) {
					t.Fatalf("Unexpected body: %s", msg.Body())
				}
				if format != models.CampaignContentTypePlain && !bytes.Equal(msg.AltBody(), appendTextFooter(original.AltBody(), plain, "000-00-00000")) {
					t.Fatalf("Missing alternative footer: %s", msg.AltBody())
				}
				if msg.headers[0]["X-Example"] != "value" {
					t.Fatal("Headers changed")
				}
				// Each MIME body makes its own duplicate decision.
				c.Body = "Existing 000-00-00000"
				c.AltBody = null.StringFrom("Alternative content")
				if err := c.CompileTemplate(nil); err != nil {
					t.Fatal(err)
				}
				msg, err = enabled.NewCampaignMessage(&c, models.Subscriber{})
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(msg.Body(), markup) || bytes.Contains(msg.Body(), plain) {
					t.Fatal("Duplicate footer")
				}
				if format != models.CampaignContentTypePlain && !bytes.Contains(msg.AltBody(), plain) {
					t.Fatal("Duplicate in HTML suppressed alternative footer")
				}
				c.Body = "Content"
				c.AltBody = null.String{}
				if err := c.CompileTemplate(nil); err != nil {
					t.Fatal(err)
				}
				msg, err = enabled.NewCampaignMessage(&c, models.Subscriber{})
				if err != nil {
					t.Fatal(err)
				}
				if len(msg.AltBody()) != 0 {
					t.Fatal("Generated absent alternative body")
				}
			})
		}
	}
}

func TestBusinessFooterDefaultTemplates(t *testing.T) {
	for _, path := range []string{"default.tpl", "default-visual.tpl"} {
		t.Run(path, func(t *testing.T) {
			body, err := os.ReadFile("../../static/email-templates/" + path)
			if err != nil {
				t.Fatal(err)
			}
			// Test insertion after the complete, unmodified upstream footer and gutter.
			got := string(insertHTMLFooter(body, []byte("BUSINESS"), ""))
			if strings.Index(got, "BUSINESS") < strings.Index(got, "{{ UnsubscribeURL }}") || !strings.Contains(got, "BUSINESS</body>") {
				t.Fatal("Wrong footer position")
			}
			if strings.Replace(got, "BUSINESS", "", 1) != string(body) {
				t.Fatal("Existing template changed")
			}
		})
	}
}

func TestBusinessFooterPreservesEmptyAltBody(t *testing.T) {
	markup, plain := BuildBusinessFooter(models.BusinessFooter{Enabled: true, CompanyName: "Example"}, testFooterLabels)
	enabled := Manager{cfg: Config{BusinessFooterHTML: markup, BusinessFooterText: plain}}
	disabled := Manager{}
	for _, format := range []string{models.CampaignContentTypeHTML, models.CampaignContentTypeRichtext, models.CampaignContentTypeMarkdown, models.CampaignContentTypeVisual} {
		for _, tc := range []struct {
			name string
			alt  null.String
		}{
			{"null", null.String{}},
			{"stored empty string", null.StringFrom("")},
			{"template renders empty", null.StringFrom(`{{ if .Subscriber.Name }}Alternative content{{ end }}`)},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				c := models.Campaign{
					ContentType: format, Messenger: "email", Body: "Content", AltBody: tc.alt,
					TemplateBody: `<body>{{ template "content" . }}</body>`,
				}
				if err := c.CompileTemplate(nil); err != nil {
					t.Fatal(err)
				}
				for _, mgr := range []*Manager{&disabled, &enabled} {
					msg, err := mgr.NewCampaignMessage(&c, models.Subscriber{})
					if err != nil {
						t.Fatal(err)
					}
					if len(msg.AltBody()) != 0 {
						t.Fatalf("Empty alternative body gained content: %q", msg.AltBody())
					}
					if mgr == &enabled && !bytes.Contains(msg.Body(), markup) {
						t.Fatal("Main HTML body lost its footer")
					}
				}
			})
		}
	}
}
