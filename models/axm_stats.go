package models

import (
	"encoding/json"

	"github.com/lib/pq"
	null "gopkg.in/volatiletech/null.v6"
)

// StatsCampaign is one campaign of the statistics dashboard (LST-64). Aggregates only: the
// lists of a campaign are included when Own is true for the caller.
type StatsCampaign struct {
	ID            int            `db:"id" json:"id"`
	Name          string         `db:"name" json:"name"`
	Subject       string         `db:"subject" json:"subject"`
	Status        string         `db:"status" json:"status"`
	StartedAt     null.Time      `db:"started_at" json:"started_at"`
	Sent          int            `db:"sent" json:"sent"`
	ToSend        int            `db:"to_send" json:"to_send"`
	ListIDs       pq.Int64Array  `db:"list_ids" json:"-"`
	ListNames     pq.StringArray `db:"list_names" json:"-"`
	ListSources   pq.StringArray `db:"list_sources" json:"-"`
	ListCreatedAt pq.StringArray `db:"list_created_at" json:"-"`
	ListsN        int            `db:"lists_n" json:"lists_n"`
	TeamTags      pq.StringArray `db:"team_tags" json:"team_tags"`
	Viewers       int            `db:"viewers" json:"viewers"`
	Clickers      int            `db:"clickers" json:"clickers"`
	Hard          int            `db:"hard" json:"hard"`
	Soft          int            `db:"soft" json:"soft"`
	Complaint     int            `db:"complaint" json:"complaint"`
	LedgerRows    int            `db:"ledger_rows" json:"ledger_rows"`
	// TrackingOff: opens and clicks were not recorded when the campaign went out.
	TrackingOff        bool             `db:"tracking_off" json:"tracking_off"`
	FollowupOf         null.Int         `db:"followup_of" json:"followup_of"`
	FollowupCondition  *json.RawMessage `db:"followup_condition" json:"followup_condition,omitempty"`
	FollowupParentSent null.Int         `db:"followup_parent_sent" json:"followup_parent_sent"`
	CombinedViewers    null.Int         `db:"combined_viewers" json:"combined_viewers"`
	CombinedClickers   null.Int         `db:"combined_clickers" json:"combined_clickers"`

	// Set by the handler for the caller.
	Own   bool        `db:"-" json:"own"`
	Lists []StatsList `db:"-" json:"lists,omitempty"`
}

// StatsList is a list of an own campaign.
type StatsList struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// StatsOverlap counts recipients of a campaign who received other campaigns shortly before it.
type StatsOverlap struct {
	ID         int `db:"id" json:"id"`
	LedgerRows int `db:"ledger_rows" json:"ledger_rows"`
	Overlap7   int `db:"overlap7" json:"overlap7"`
	Overlap30  int `db:"overlap30" json:"overlap30"`
}

// StatsRecipientGroup is an aggregate for a team ("" is everyone). Groups below
// StatsMinGroup unique recipients use StatsOtherKey.
type StatsRecipientGroup struct {
	Team       string `db:"team" json:"team"`
	Key        string `db:"key" json:"key"`
	Sent       int    `db:"sent" json:"sent"`
	Recipients int    `db:"recipients" json:"recipients"`
	Opened     int    `db:"opened" json:"opened"`
	Clicked    int    `db:"clicked" json:"clicked"`
	Bounced    int    `db:"bounced" json:"bounced"`
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
	StatsMinGroup        = 30
	StatsOtherKey        = "__other__"
	StatsNoTeamKey       = "__none__"
	StatsSourceTagPrefix = "출처:"
)
