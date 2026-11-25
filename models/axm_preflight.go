package models

import (
	"time"

	"gopkg.in/volatiletech/null.v6"
)

// Modes of the pre-send confirmation (LST-48): what starting the campaign does now.
const (
	PreflightModeStart    = "start"
	PreflightModeSchedule = "schedule"
	PreflightModeResume   = "resume"
	// PreflightModeScheduled (LST-67): a campaign that is already scheduled, read again after it
	// was edited. Nothing is asked for; it goes out at its send time as it is.
	PreflightModeScheduled = "scheduled"
)

// Levels of a pre-send check. Only "block" disables the confirmation.
const (
	PreflightOK    = "ok"
	PreflightWarn  = "warn"
	PreflightBlock = "block"
)

// PreflightList is one list of the campaign and the people it alone would reach.
type PreflightList struct {
	ID      null.Int `db:"id" json:"id"`
	Name    string   `db:"name" json:"name"`
	Optin   string   `db:"optin" json:"optin"`
	Dynamic bool     `db:"dynamic" json:"dynamic"`
	Deleted bool     `db:"deleted" json:"deleted"`
	Count   int      `db:"count" json:"count"`
}

// PreflightAudience is the people a campaign reaches across all lists, each counted once.
type PreflightAudience struct {
	Total int `db:"total" json:"total"`
	// Multi is how many of them are on more than one list; Excluded how many people on the
	// lists are left out (blocklisted, unsubscribed, not confirmed).
	Multi     int       `db:"multi" json:"multi"`
	Excluded  int       `db:"excluded" json:"excluded"`
	Remaining int       `db:"remaining" json:"remaining"`
	AsOf      time.Time `db:"as_of" json:"as_of"`
}

// PreflightRow is a row of axm-preflight-audience: a list and the totals of all lists.
type PreflightRow struct {
	PreflightList
	PreflightAudience
}

// PreflightCheck is one line of "checks before sending". The screen translates Key (with
// its Level) and Params.
type PreflightCheck struct {
	Key    string         `json:"key"`
	Level  string         `json:"level"`
	Params map[string]any `json:"params,omitempty"`
}
