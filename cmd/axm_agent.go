package main

import (
	"net/http"
	"strings"

	"github.com/knadh/listmonk/models"
)

// Classification of the client behind a view pixel or a tracked link request (AXM send
// records). Only the resulting class is stored. The user-agent string and the IP are
// read here and then thrown away.
//
// The class says what kind of program made the request, not whether a person read the
// message: an image proxy fetches for a person or ahead of one. Later analysis combines
// it with the time since sending and the counts on the send record.

var (
	// Request headers with which a client says it is fetching ahead of the user.
	agentPrefetchHeaders = []string{"Purpose", "X-Purpose", "Sec-Purpose", "X-Moz"}
	agentPrefetchValues  = []string{"prefetch", "prerender", "preview"}

	// Mailbox providers' image proxies other than Gmail and Apple.
	agentMailProxies = []string{"yahoomailproxy"}

	// Scripts, crawlers, link previewers and mail security scanners that name themselves.
	// Scanners that pose as a browser cannot be told apart here.
	agentAutomated = []string{
		"bot", "crawler", "spider", "scanner", "headlesschrome", "phantomjs",
		"curl/", "wget/", "python-requests", "python-urllib", "aiohttp", "go-http-client",
		"okhttp", "java/", "apache-httpclient", "libwww", "node-fetch", "axios/", "guzzlehttp",
		"facebookexternalhit", "kakaotalk-scrap", "whatsapp", "slack-imgproxy", "skypeuripreview",
		"proofpoint", "barracuda", "mimecast", "symantec", "trendmicro", "fireeye",
	}

	// Desktop mail programs that fetch remote content themselves.
	agentMailClients = []string{
		"microsoft outlook", "microsoft office", "ms-office", "thunderbird",
		"postbox", "emclient", "mailbird", "lotus-notes", "airmail",
	}

	// Rendering engines and browsers named in a Mozilla-style user-agent.
	agentBrowserTokens = []string{
		"applewebkit", "gecko/", "trident/", "msie ", "chrome/", "firefox/", "safari/", "presto/",
	}
)

// classifyAgent returns the models.AgentKind* class of a tracking request.
func classifyAgent(h http.Header) int {
	for _, name := range agentPrefetchHeaders {
		if v := strings.ToLower(h.Get(name)); v != "" && containsAny(v, agentPrefetchValues) {
			return models.AgentKindAutomated
		}
	}

	ua := strings.ToLower(strings.TrimSpace(h.Get("User-Agent")))
	switch {
	case ua == "":
		return models.AgentKindNone

	// Gmail's proxy appends "(via ggpht.com GoogleImageProxy)" to a browser-like string.
	case strings.Contains(ua, "googleimageproxy"):
		return models.AgentKindGmailProxy

	// Apple Mail Privacy Protection is reported to send exactly this and nothing more.
	case ua == "mozilla/5.0":
		return models.AgentKindAppleProxy

	case containsAny(ua, agentMailProxies):
		return models.AgentKindMailProxy

	// Before the browser check: headless browsers and Outlook also carry browser tokens.
	case containsAny(ua, agentAutomated):
		return models.AgentKindAutomated

	case containsAny(ua, agentMailClients):
		return models.AgentKindMailClient

	case (strings.HasPrefix(ua, "mozilla/") && containsAny(ua, agentBrowserTokens)) || strings.HasPrefix(ua, "opera/"):
		return models.AgentKindBrowser
	}

	return models.AgentKindOther
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
