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
