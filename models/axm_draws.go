package models

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx/types"
)

// Draw size units and the scope of the "already received" exclusion.
const (
	DrawUnitCount   = "count"
	DrawUnitPercent = "percent"

	DrawScopeSameSource = "same_source"
	DrawScopeAll        = "all"

	// DrawListTag is the tag every drawn list gets, next to the tags of its source.
	DrawListTag = "뽑은-리스트"

	// DrawMaxCount bounds a count draw. DrawMaxDays bounds the look back of the exclusion.
	DrawMaxCount = 10000000
	DrawMaxDays  = 3650
)

// DrawSettings is what a draw is asked to do. It is stored with the draw record.
type DrawSettings struct {
	Unit  string `json:"unit"`
	Value int    `json:"value"`

	// ExcludeScope is DrawScopeSameSource or DrawScopeAll. ExcludeDays is 0 for the whole
	// time the send records are kept, or the number of days to look back.
	ExcludeScope string `json:"exclude_scope"`
	ExcludeDays  int    `json:"exclude_days"`
}

// Validate checks the settings and fills the defaults of the scope.
func (s *DrawSettings) Validate() error {
	switch s.Unit {
	case DrawUnitCount:
		if s.Value < 1 || s.Value > DrawMaxCount {
			return fmt.Errorf("count must be between 1 and %d", DrawMaxCount)
		}
	case DrawUnitPercent:
		if s.Value < 1 || s.Value > 100 {
			return fmt.Errorf("percent must be between 1 and 100")
		}
	default:
		return fmt.Errorf("unit must be %q or %q", DrawUnitCount, DrawUnitPercent)
	}
	if s.ExcludeScope == "" {
		s.ExcludeScope = DrawScopeSameSource
	}
	if s.ExcludeScope != DrawScopeSameSource && s.ExcludeScope != DrawScopeAll {
		return fmt.Errorf("exclude_scope must be %q or %q", DrawScopeSameSource, DrawScopeAll)
	}
	if s.ExcludeDays < 0 || s.ExcludeDays > DrawMaxDays {
		return fmt.Errorf("exclude_days must be between 0 and %d", DrawMaxDays)
	}
	return nil
}

// Want is how many people the settings ask for out of a target of the given size.
func (s DrawSettings) Want(target int) int {
	if s.Unit == DrawUnitPercent {
		// Rounded up, in integers so no float rounding decides who is drawn.
		return int((int64(target)*int64(s.Value) + 99) / 100)
	}
	return s.Value
}

// DrawCounts are the numbers of a draw: the people the source targets now, how many of
// them are left out and why, and how many were (or would be) drawn.
type DrawCounts struct {
	Target          int `db:"target" json:"target"`
	AlreadyDrawn    int `db:"already_drawn" json:"already_drawn"`
	AlreadyReceived int `db:"already_received" json:"already_received"`
	Candidates      int `db:"candidates" json:"candidates"`
	// Requested is what the settings ask for, WillDraw what the candidates allow.
	Requested int `db:"-" json:"requested"`
	WillDraw  int `db:"-" json:"will_draw"`
}

// ListDraw is the record of one list made by a draw.
type ListDraw struct {
	ID           int            `db:"id" json:"id"`
	ListID       *int           `db:"list_id" json:"list_id"`
	SourceListID *int           `db:"source_list_id" json:"source_list_id"`
	SourceName   string         `db:"source_name" json:"source_name"`
	ListName     string         `db:"list_name" json:"list_name"`
	DrawnAt      time.Time      `db:"drawn_at" json:"drawn_at"`
	DrawnBy      *int           `db:"drawn_by" json:"drawn_by"`
	DrawnByName  string         `db:"drawn_by_name" json:"drawn_by_name"`
	Settings     types.JSONText `db:"settings" json:"settings"`
	Result       types.JSONText `db:"result" json:"result"`
	DeletedAt    *time.Time     `db:"deleted_at" json:"deleted_at"`

	// Campaigns counts the campaigns that target the drawn list, Finished those that are
	// done. Both are 0 once the list is deleted.
	Campaigns         int `db:"campaigns" json:"campaigns"`
	CampaignsFinished int `db:"campaigns_finished" json:"campaigns_finished"`
}
