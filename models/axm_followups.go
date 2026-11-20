package models

import (
	"encoding/json"
	"time"

	null "gopkg.in/volatiletech/null.v6"
)

const (
	// FollowupTag marks a campaign made as the follow-up of another one.
	FollowupTag = "후속"

	// FollowupWarnDays is how many days after the last send of the original campaign the
	// follow-up dialog stops warning that many people may still open it. It only warns.
	FollowupWarnDays = 3
)

// FollowupRef names the other campaign of a link.
type FollowupRef struct {
	ID     int    `db:"id" json:"id"`
	Name   string `db:"name" json:"name"`
	Status string `db:"status" json:"status"`
}

// ReactionFollowup tells the reaction dialog whether it may offer the follow-up draft.
type ReactionFollowup struct {
	Available   bool         `json:"available"`
	Reason      string       `json:"reason,omitempty"`
	Existing    *FollowupRef `json:"existing,omitempty"`
	DaysSince   *int         `json:"days_since"`
	WarnDays    int          `json:"warn_days"`
	DefaultName string       `json:"default_name"`
}

// FollowupRecheck is what the checks before a send removed from the follow-up list so far.
type FollowupRecheck struct {
	Reacted  int `json:"reacted"`
	OptedOut int `json:"opted_out"`
	Blocked  int `json:"blocked"`
	Runs     int `json:"runs"`
}

// FollowupPending is who would leave the follow-up list if the check ran now.
type FollowupPending struct {
	Reacted  int `db:"reacted" json:"reacted"`
	OptedOut int `db:"opted_out" json:"opted_out"`
	Blocked  int `db:"blocked" json:"blocked"`
}

// FollowupRow is one link, joined with both campaigns and the follow-up list.
type FollowupRow struct {
	Role             string          `db:"role"`
	CampaignID       int             `db:"campaign_id"`
	ParentCampaignID int             `db:"parent_campaign_id"`
	ListID           null.Int        `db:"list_id"`
	Condition        json.RawMessage `db:"condition"`
	Counts           json.RawMessage `db:"counts"`
	Recheck          json.RawMessage `db:"recheck"`
	RecheckedAt      null.Time       `db:"rechecked_at"`
	CreatedAt        time.Time       `db:"created_at"`
	FollowupName     string          `db:"followup_name"`
	FollowupStatus   string          `db:"followup_status"`
	FollowupSendAt   null.Time       `db:"followup_send_at"`
	ParentName       string          `db:"parent_name"`
	ParentStatus     string          `db:"parent_status"`
	ListName         null.String     `db:"list_name"`
	ListCount        int             `db:"list_count"`
	CampaignLists    int             `db:"campaign_lists"`
	HasList          bool            `db:"has_list"`
}

// FollowupInfo is the answer of GET /api/campaigns/:id/followup.
type FollowupInfo struct {
	Role         string            `json:"role"` // none, parent or followup
	Parent       *FollowupRef      `json:"parent,omitempty"`
	Followup     *FollowupRef      `json:"followup,omitempty"`
	Condition    *ReactionSettings `json:"condition,omitempty"`
	Counts       json.RawMessage   `json:"counts,omitempty"`
	Recheck      *FollowupRecheck  `json:"recheck,omitempty"`
	RecheckedAt  *time.Time        `json:"rechecked_at,omitempty"`
	CreatedAt    *time.Time        `json:"created_at,omitempty"`
	List         *FollowupList     `json:"list,omitempty"`
	ListsChanged bool              `json:"lists_changed"`
	Pending      *FollowupPending  `json:"pending,omitempty"`
}

type FollowupList struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}
