package main

import (
	"net/http"
	"testing"

	"github.com/knadh/listmonk/models"
)

func TestClassifyAgent(t *testing.T) {
	cases := []struct {
		agent string
		want  int
	}{
		{"", models.AgentKindNone},
		{"   ", models.AgentKindNone},

		// Mailbox providers' image proxies.
		{"Mozilla/5.0 (Windows NT 5.1; rv:11.0) Gecko Firefox/11.0 (via ggpht.com GoogleImageProxy)", models.AgentKindGmailProxy},
		{"Mozilla/5.0", models.AgentKindAppleProxy},
		{" mozilla/5.0 ", models.AgentKindAppleProxy},
		{"YahooMailProxy; https://help.yahoo.com/kb/yahoo-mail-proxy-SLN28749.html", models.AgentKindMailProxy},

		// Browsers and mail apps that render with a browser engine.
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", models.AgentKindBrowser},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)", models.AgentKindBrowser},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148", models.AgentKindBrowser},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0", models.AgentKindBrowser},
		{"Mozilla/5.0 (Linux; Android 14; SM-S921N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36 NAVER(inapp; search; 2000; 12.9.5)", models.AgentKindBrowser},
		{"Opera/9.80 (Windows NT 6.1) Presto/2.12.388 Version/12.18", models.AgentKindBrowser},

		// Desktop mail programs. Outlook also carries browser tokens.
		{"Microsoft Office/16.0 (Windows NT 10.0; Microsoft Outlook 16.0.17928; Pro)", models.AgentKindMailClient},
		{"Mozilla/4.0 (compatible; MSIE 7.0; Windows NT 10.0; Trident/7.0; Microsoft Outlook 16.0.17928; ms-office; MSOffice 16)", models.AgentKindMailClient},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Thunderbird/128.4.0", models.AgentKindMailClient},

		// Scripts, crawlers, previewers and scanners, including ones that look like a browser.
		{"curl/8.5.0", models.AgentKindAutomated},
		{"python-requests/2.32.3", models.AgentKindAutomated},
		{"Go-http-client/2.0", models.AgentKindAutomated},
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/130.0.0.0 Safari/537.36", models.AgentKindAutomated},
		{"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", models.AgentKindAutomated},
		{"facebookexternalhit/1.1; kakaotalk-scrap/1.0; +https://devtalk.kakao.com/t/scrap/33984", models.AgentKindAutomated},
		{"Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)", models.AgentKindAutomated},

		// Anything else.
		{"Mozilla/5.0 (compatible)", models.AgentKindOther},
		{"MyMailApp/3.1", models.AgentKindOther},
	}
	for _, tc := range cases {
		h := http.Header{}
		if tc.agent != "" {
			h.Set("User-Agent", tc.agent)
		}
		if got := classifyAgent(h); got != tc.want {
			t.Errorf("%q: got %d, want %d", tc.agent, got, tc.want)
		}
	}

	// A request that says it is fetching ahead of the user is automated whatever its agent.
	const chrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	for name, value := range map[string]string{"Sec-Purpose": "prefetch;prerender", "Purpose": "prefetch", "X-Purpose": "preview", "X-Moz": "prefetch"} {
		h := http.Header{}
		h.Set("User-Agent", chrome)
		h.Set(name, value)
		if got := classifyAgent(h); got != models.AgentKindAutomated {
			t.Errorf("%s: %s: got %d", name, value, got)
		}
	}
	h := http.Header{}
	h.Set("User-Agent", chrome)
	h.Set("Purpose", "navigation")
	if got := classifyAgent(h); got != models.AgentKindBrowser {
		t.Errorf("an unrelated Purpose header changed the class to %d", got)
	}

	// Every class fits the SMALLINT bitmask the queries build with 1 << kind.
	for _, k := range []int{models.AgentKindNone, models.AgentKindBrowser, models.AgentKindAppleProxy, models.AgentKindGmailProxy,
		models.AgentKindMailProxy, models.AgentKindMailClient, models.AgentKindAutomated, models.AgentKindOther} {
		if k < 0 || k > 14 {
			t.Errorf("class %d does not fit the bitmask", k)
		}
	}
}
