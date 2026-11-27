package models

import (
	"time"

	"github.com/lib/pq"
	"gopkg.in/volatiletech/null.v6"
)

const (
	RecentScopeAll       = "all"
	RecentScopeTeam      = "team"
	RecentScopeCampaigns = "campaigns"

	RecentMaxCampaigns = 20

	// RecentListTag marks a list made from a recent-send exclusion result.
	RecentListTag = "제외-적용-리스트"
)

var RecentDays = [...]int{7, 14, 30}

// RecentRule is the saved preview configuration. An absent row means disabled.
type RecentRule struct {
	Enabled     bool          `db:"enabled" json:"enabled"`
	Days        int           `db:"days" json:"days,omitempty"`
	Scope       string        `db:"scope" json:"scope,omitempty"`
	CampaignIDs pq.Int64Array `db:"campaign_ids" json:"campaign_ids,omitempty"`
	UpdatedAt   *time.Time    `db:"updated_at" json:"updated_at,omitempty"`
	UpdatedBy   *int          `db:"updated_by" json:"updated_by,omitempty"`
	// Held (LST-65) is how many people the rule has left out of the campaign's send so far.
	Held int `db:"-" json:"held,omitempty"`
	// RecordsFrom is present when the finished campaign editor asks for ledger coverage.
	// A nil pointer means coverage was not requested. An invalid time means the ledger is empty.
	RecordsFrom *null.Time `db:"-" json:"records_from,omitempty"`
}

type RecentCounts struct {
	Target      int       `db:"target" json:"target"`
	Excluded    int       `db:"excluded" json:"excluded"`
	WillSend    int       `db:"-" json:"will_send"`
	AsOf        time.Time `db:"as_of" json:"as_of"`
	RecordsFrom null.Time `db:"-" json:"records_from"`
	TeamTags    []string  `db:"-" json:"team_tags"`
}

// CanEditCampaign reports whether the campaign's settings can be updated.
func CanEditCampaign(status string) bool {
	return status == CampaignStatusDraft || status == CampaignStatusPaused || status == CampaignStatusScheduled
}

// CanEditRecentRule reports whether the recent-send rule of a campaign can be changed (LST-65): only
// before the campaign starts. Once it has started the rule decides who it leaves out, so a pause does
// not open it (turning it off would let the people it left out in again).
func CanEditRecentRule(status string, started bool) bool {
	return !started && (status == CampaignStatusDraft || status == CampaignStatusScheduled)
}

// RecentSourceList is one list the exclusion result was made from.
type RecentSourceList struct {
	ID     int            `db:"id" json:"id"`
	Name   string         `db:"name" json:"name"`
	Tags   pq.StringArray `db:"tags" json:"-"`
	Preset string         `db:"axm_preset" json:"-"`
}

// RecentListCounts is the audience, the excluded people and the people put on the new list.
type RecentListCounts struct {
	Target   int `json:"target"`
	Excluded int `json:"excluded"`
	Added    int `json:"added"`
}

// RecentListResult is the list made from an exclusion result and what it was made from.
type RecentListResult struct {
	List        List               `json:"list"`
	Counts      RecentListCounts   `json:"counts"`
	AsOf        time.Time          `json:"as_of"`
	SourceLists []RecentSourceList `json:"source_lists"`
}
