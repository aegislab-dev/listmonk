package main

import (
	"bytes"
	"html/template"
	"os"
	"strings"
	"testing"

	"github.com/knadh/listmonk/internal/i18n"
	"github.com/knadh/listmonk/internal/manager"
	"github.com/knadh/listmonk/models"
	"github.com/knadh/stuffbin"
)

// AXM (LST-76): the public pages show the business details of the setting
// axm.business_footer above the powered-by line, and nothing while it is off.
func TestPublicFooterBusinessDetails(t *testing.T) {
	en, err := os.ReadFile("../i18n/en.json")
	if err != nil {
		t.Fatal(err)
	}
	ko, err := os.ReadFile("../i18n/ko.json")
	if err != nil {
		t.Fatal(err)
	}
	i, err := i18n.New(en)
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Load(ko); err != nil {
		t.Fatal(err)
	}
	tpl, err := template.New("").Funcs(initTplFuncs(i, &UrlConfig{RootURL: "http://listmonk.invalid"})).ParseFiles("../static/public/templates/index.html")
	if err != nil {
		t.Fatal(err)
	}
	// The labels are English whatever the instance language (LST-76 v2).
	enOnly, err := i18n.New(en)
	if err != nil {
		t.Fatal(err)
	}
	render := func(info models.BusinessFooter) string {
		var b bytes.Buffer
		if err := tpl.ExecuteTemplate(&b, "footer", tplData{L: i, BusinessFooter: manager.NewBusinessFooterBlock(info, businessFooterLabels(enOnly))}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	info := models.BusinessFooter{
		Enabled: true, CompanyName: "Example <b>Inc.</b>", Representative: "Gildong Hong", RegistrationNo: "000-00-00000",
		Address: "1 Example-ro, Example-gu, Seoul", Phone: "+82-0-0000-0000", Email: "help@example.invalid", Extra: "First line\nSecond <i>line</i>",
	}
	out := render(info)
	for _, want := range []string{
		`<div class="business-info">`,
		`<span>Example &lt;b&gt;Inc.&lt;/b&gt;</span> &middot; <span class="nowrap">Representative Gildong Hong</span> &middot; <span class="nowrap">Business registration no. 000-00-00000</span>`,
		`<span>1 Example-ro, Example-gu, Seoul</span>`,
		// html/template writes "+" as &#43;, which the browser shows as "+".
		`<span class="nowrap">Phone &#43;82-0-0000-0000</span> &middot; <span class="nowrap">Email help@example.invalid</span>`,
		`<p class="extra">First line<br />Second &lt;i&gt;line&lt;/i&gt;</p>`,
		"제공:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("footer misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<b>") || strings.Contains(out, "<i>") {
		t.Errorf("unescaped value in the footer:\n%s", out)
	}
	if strings.Index(out, "business-info") > strings.Index(out, "제공:") {
		t.Errorf("the business details must come before the powered-by line:\n%s", out)
	}

	info.Enabled = false
	if out := render(info); strings.Contains(out, "business-info") || strings.Contains(out, "Gildong") {
		t.Errorf("a footer that is off must not show:\n%s", out)
	}
}

// AXM (LST-76 v2): the labels come from the English file even on a Korean instance.
func TestBusinessFooterLabelsAreEnglish(t *testing.T) {
	saved := fs
	defer func() { fs = saved }()

	var err error
	fs, err = stuffbin.NewLocalFS("/", "../i18n/en.json:/i18n/en.json", "../i18n/ko.json:/i18n/ko.json")
	if err != nil {
		t.Fatal(err)
	}
	ko := initI18n("ko", fs)
	if ko.T("email.businessFooter.representative") == "Representative" {
		t.Fatal("the Korean file must have its own label for this test to mean anything")
	}
	got := businessFooterLabels(businessFooterI18n(ko))
	want := manager.BusinessFooterLabels{Representative: "Representative", RegistrationNo: "Business registration no.", Phone: "Phone", Email: "Email"}
	if got != want {
		t.Fatalf("labels = %+v, want %+v", got, want)
	}
}
