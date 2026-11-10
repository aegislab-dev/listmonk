package main

import (
	"log"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/labstack/echo/v4"
)

// crawlerUserAgents is the single list used by both robots.txt and HTTP blocking.
// Sources (reviewed 2026-11-10):
// https://developers.openai.com/api/docs/bots
// https://support.claude.com/en/articles/8896518
// https://commoncrawl.org/ccbot
// https://docs.perplexity.ai/docs/resources/perplexity-crawlers
// https://developer.amazon.com/amazonbot
// https://developers.cloudflare.com/ai-crawl-control/reference/bots/ (Bytespider, Meta)
// https://developers.google.com/search/docs/crawling-indexing/google-common-crawlers
// https://support.apple.com/en-us/119829
// Claude-Web and anthropic-ai are legacy names retained by the LST-6 policy.
// Google-Extended and Applebot-Extended are robots control tokens, not separate
// HTTP crawlers; they cannot prevent AI use of Googlebot/Applebot content by UA.
const crawlerUserAgents = `
GPTBot
ChatGPT-User
OAI-SearchBot
ClaudeBot
Claude-Web
anthropic-ai
Claude-User
Claude-SearchBot
CCBot
Google-Extended
PerplexityBot
Perplexity-User
Bytespider
Amazonbot
Applebot-Extended
meta-externalagent
meta-externalfetcher
`

// blockCrawlers is attached only to public content routes. Subscriber actions,
// link redirects, tracking pixels, admin/auth and APIs keep their own handlers.
// The public archive API is content too, so it uses the same protection.
func blockCrawlers(logger *log.Logger) echo.MiddlewareFunc {
	agents := strings.Fields(strings.ToLower(crawlerUserAgents))
	var blocked atomic.Uint64

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Add("Vary", "User-Agent")
			ua := strings.ToLower(c.Request().UserAgent())
			for _, agent := range agents {
				if strings.Contains(ua, agent) {
					// Count only: never log UA, IP, URL, query or subscriber identifiers.
					logger.Printf("blocked crawler requests: count=%d", blocked.Add(1))
					return c.String(http.StatusForbidden, "Forbidden\n")
				}
			}
			return next(c)
		}
	}
}
