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
