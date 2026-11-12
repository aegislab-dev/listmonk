package models

import "time"

// CampaignSend is one campaign message that a messenger accepted for a subscriber.
type CampaignSend struct {
	CampaignID   int
	SubscriberID int

	// SentAt is the moment the messenger returned. It is read with the monotonic
	// clock to work out the message's age when the record is written.
	SentAt time.Time
}

// Client classes of the request that opened a message or clicked a link in it. They
// are stored in axm_campaign_sends.open_kind / click_kind, and as bits (1 << kind) in
// open_kinds / click_kinds. Only this class is kept: the user-agent string and the IP
// are not stored. The numbers are stored data, so do not renumber them.
const (
	// AgentKindNone is a request with no user-agent.
	AgentKindNone = 0

	// AgentKindBrowser is a browser or a mail app's web view fetching directly.
	AgentKindBrowser = 1

	// AgentKindAppleProxy is the bare "Mozilla/5.0" that Apple Mail Privacy Protection
	// is reported to send when it preloads images.
	AgentKindAppleProxy = 2

	// AgentKindGmailProxy is Gmail's image proxy (GoogleImageProxy).
	AgentKindGmailProxy = 3

	// AgentKindMailProxy is another mailbox provider's image proxy (YahooMailProxy).
	AgentKindMailProxy = 4

	// AgentKindMailClient is a desktop mail program fetching directly (Outlook, Thunderbird).
	AgentKindMailClient = 5

	// AgentKindAutomated is a script, crawler, link previewer, security scanner or
	// a request that says it is a prefetch.
	AgentKindAutomated = 6

	// AgentKindOther is a user-agent that fits none of the above.
	AgentKindOther = 7
)
