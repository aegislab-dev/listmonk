package models

import (
	"time"

	"github.com/lib/pq"
	null "gopkg.in/volatiletech/null.v6"
)

// StatsCampaign is one campaign of the statistics dashboard (LST-64). Aggregates only: the
// lists of a campaign are named only to people who may read the campaign (Own).
type StatsCampaign struct {
	ID         int            `db:"id" json:"id"`
	Name       string         `db:"name" json:"name"`
	Subject    string         `db:"subject" json:"subject"`
	Status     string         `db:"status" json:"status"`
	StartedAt  null.Time      `db:"started_at" json:"started_at"`
	Sent       int            `db:"sent" json:"sent"`
	ToSend     int            `db:"to_send" json:"to_send"`
	ListIDs    pq.Int64Array  `db:"list_ids" json:"-"`
	ListNames  pq.StringArray `db:"list_names" json:"-"`
	ListsN     int            `db:"lists_n" json:"lists_n"`
	TeamTags   pq.StringArray `db:"team_tags" json:"team_tags"`
	Viewers    int            `db:"viewers" json:"viewers"`
	Clickers   int            `db:"clickers" json:"clickers"`
	Hard       int            `db:"hard" json:"hard"`
	Soft       int            `db:"soft" json:"soft"`
	Complaint  int            `db:"complaint" json:"complaint"`
	LedgerRows int            `db:"ledger_rows" json:"ledger_rows"`
	Overlap30  int            `db:"overlap30" json:"overlap30"`
	// TrackingOff: opens and clicks were not recorded when the campaign went out.
	TrackingOff bool     `db:"tracking_off" json:"tracking_off"`
	FollowupOf  null.Int `db:"followup_of" json:"followup_of"`

	// Set by the handler for the caller.
	Own   bool        `db:"-" json:"own"`
	Lists []StatsList `db:"-" json:"lists,omitempty"`
}

// StatsList is a list of an own campaign.
type StatsList struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// StatsRecipientRow is one group of recipients of one campaign.
type StatsRecipientRow struct {
	CampaignID int    `db:"campaign_id" json:"-"`
	Key        string `db:"key" json:"key"`
	Sent       int    `db:"sent" json:"sent"`
	Opened     int    `db:"opened" json:"opened"`
	Clicked    int    `db:"clicked" json:"clicked"`
	Bounced    int    `db:"bounced" json:"bounced"`
}

// StatsRecipientGroup is a group of recipients summed over the campaigns of a team (Team ""
// is everyone). Groups of fewer than StatsMinGroup people are merged into StatsOtherKey.
type StatsRecipientGroup struct {
	Team    string `json:"team"`
	Key     string `json:"key"`
	Sent    int    `json:"sent"`
	Opened  int    `json:"opened"`
	Clicked int    `json:"clicked"`
	Bounced int    `json:"bounced"`
}

// StatsTeam is a team role and the number of lists that carry its tag.
type StatsTeam struct {
	Name  string `db:"name" json:"name"`
	Tag   string `db:"tag" json:"tag"`
	Lists int    `db:"lists" json:"lists"`
}

// Recipient groups smaller than StatsMinGroup are shown as "other" so that a small group
// cannot point at a person.
const (
	StatsMinGroup = 30
	StatsOtherKey = "__other__"
)

// StatsWindowMax is the longest range the statistics read.
const StatsWindowMax = 800 * 24 * time.Hour
