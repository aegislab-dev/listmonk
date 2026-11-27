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
	render := func(info models.BusinessFooter) string {
		var b bytes.Buffer
		if err := tpl.ExecuteTemplate(&b, "footer", tplData{L: i, BusinessFooter: manager.NewBusinessFooterBlock(info, businessFooterLabels(i))}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	info := models.BusinessFooter{
		Enabled: true, CompanyName: "예시 <b>주식회사</b>", Representative: "홍길동", RegistrationNo: "000-00-00000",
		Address: "서울특별시 예시구 예시로 1", Phone: "000-0000-0000", Email: "help@example.invalid", Extra: "첫 줄\n둘째 <i>줄</i>",
	}
	out := render(info)
	for _, want := range []string{
		`<div class="business-info">`,
		`<span>예시 &lt;b&gt;주식회사&lt;/b&gt;</span> &middot; <span class="nowrap">대표 홍길동</span> &middot; <span class="nowrap">사업자등록번호 000-00-00000</span>`,
		`<span>서울특별시 예시구 예시로 1</span>`,
		`<span class="nowrap">전화 000-0000-0000</span> &middot; <span class="nowrap">이메일 help@example.invalid</span>`,
		`<p class="extra">첫 줄<br />둘째 &lt;i&gt;줄&lt;/i&gt;</p>`,
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
	if out := render(info); strings.Contains(out, "business-info") || strings.Contains(out, "홍길동") {
		t.Errorf("a footer that is off must not show:\n%s", out)
	}
}
