package models

import (
	"fmt"
	"time"
)

const (
	ReactionAny = "any"
	ReactionYes = "yes"
	ReactionNo  = "no"

	ReactionListTag = "반응-리스트"
)

// ReactionSettings combines open and click conditions over the campaign's send records.
type ReactionSettings struct {
	Open  string `json:"open"`
	Click string `json:"click"`
}

func (s *ReactionSettings) Validate() error {
	if s.Open == "" {
		s.Open = ReactionAny
	}
	if s.Click == "" {
		s.Click = ReactionAny
	}
	for _, value := range []string{s.Open, s.Click} {
		switch value {
		case ReactionAny, ReactionYes, ReactionNo:
		default:
			return fmt.Errorf("invalid reaction condition %q", value)
		}
	}
	return nil
}

type ReactionTracking struct {
	Sends    int `db:"sends" json:"sends"`
	Viewers  int `db:"viewers" json:"viewers"`
	Clickers int `db:"clickers" json:"clickers"`
	// LastSentAt is when the last message of the campaign was accepted (LST-47).
	LastSentAt         *time.Time `db:"last_sent_at" json:"last_sent_at"`
	IndividualTracking bool       `db:"-" json:"individual_tracking"`
}

type ReactionCampaign struct {
	ID        int        `db:"id" json:"id"`
	Name      string     `db:"name" json:"name"`
	Status    string     `db:"status" json:"status"`
	Type      string     `db:"type" json:"type"`
	StartedAt *time.Time `db:"started_at" json:"started_at"`
}

type ReactionCounts struct {
	Matched        int `db:"matched" json:"matched"`
	Blocklisted    int `db:"blocklisted" json:"blocklisted"`
	CampaignOptOut int `db:"campaign_opt_out" json:"campaign_opt_out"`
	AllOptOut      int `db:"all_opt_out" json:"all_opt_out"`
	OutOfScope     int `db:"out_of_scope" json:"out_of_scope"`
	Eligible       int `db:"eligible" json:"eligible"`
}

type ReactionSample struct {
	Email string `db:"email" json:"email"`
	Name  string `db:"name" json:"name"`
}

type ReactionPreview struct {
	Campaign    ReactionCampaign `json:"campaign"`
	Tracking    ReactionTracking `json:"tracking"`
	SourceTeams []string         `json:"source_teams"`
	Counts      *ReactionCounts  `json:"counts,omitempty"`
	Sample      []ReactionSample `json:"sample"`
	DefaultName string           `json:"default_name,omitempty"`
	CheckedAt   time.Time        `json:"checked_at"`
	// Followup says whether the follow-up draft can be offered with this list (LST-47).
	Followup *ReactionFollowup `json:"followup,omitempty"`
}

type ReactionResult struct {
	List      List             `json:"list"`
	Campaign  ReactionCampaign `json:"campaign"`
	Tracking  ReactionTracking `json:"tracking"`
	Counts    ReactionCounts   `json:"counts"`
	CheckedAt time.Time        `json:"checked_at"`
	// Followup is the draft campaign made together with the list, when it was asked for.
	Followup *FollowupRef `json:"followup,omitempty"`
}

// WithReactionCandidates composes the shared eligibility CTE with the three statements
// that consume it. Both fragments come from the named SQL files.
func WithReactionCandidates(name, query, candidates string) string {
	switch name {
	case "axm-reaction-counts", "axm-reaction-sample", "axm-reaction-add-members",
		"axm-followup-recheck", "axm-followup-pending":
		return candidates + "\n" + query
	default:
		return query
	}
}
