package models

import (
	"time"

	"github.com/jmoiron/sqlx/types"
	null "gopkg.in/volatiletech/null.v6"
)

// AuditEntry is one row of the audit log (axm_audit_log): who did what to what, and when.
// It never carries a subscriber's e-mail, name or attribute values.
// AuditActionFollowupCheck is the check before a follow-up campaign is sent (LST-47), written
// by the server (internal/core) with who it held back.
const AuditActionFollowupCheck = "campaign.followup_check"

type AuditEntry struct {
	ID         int64          `db:"id" json:"id"`
	CreatedAt  time.Time      `db:"created_at" json:"created_at"`
	UserID     null.Int       `db:"user_id" json:"user_id"`
	Username   string         `db:"username" json:"username"`
	UserName   string         `db:"user_name" json:"user_name"`
	UserType   string         `db:"user_type" json:"user_type"`
	Action     string         `db:"action" json:"action"`
	TargetType string         `db:"target_type" json:"target_type"`
	TargetID   null.Int       `db:"target_id" json:"target_id"`
	TargetName string         `db:"target_name" json:"target_name"`
	Count      null.Int       `db:"count" json:"count"`
	Detail     types.JSONText `db:"detail" json:"detail"`

	// Total is the number of rows that match the filter, on every row of a page.
	Total int `db:"total" json:"-"`
}

// AuditUser is a person who appears in the audit log.
type AuditUser struct {
	UserID   int    `db:"user_id" json:"user_id"`
	Username string `db:"username" json:"username"`
	UserName string `db:"user_name" json:"user_name"`
}

// AuditFilter is what the audit screen asks for.
type AuditFilter struct {
	From, To *time.Time
	UserID   *int
	Areas    []string
	Actions  []string
	Query    string
	Offset   int
	Limit    int
}

// Names of the actors that are not a signed-in person.
const (
	AuditUserTypeSystem = "system"
)

// DeletedList is a list in the trash (axm_deleted_lists).
type DeletedList struct {
	ListID            int            `db:"list_id" json:"id"`
	UUID              string         `db:"uuid" json:"uuid"`
	Name              string         `db:"name" json:"name"`
	SubCount          int            `db:"sub_count" json:"subscriber_count"`
	DeletedBy         null.Int       `db:"deleted_by" json:"deleted_by"`
	DeletedByUsername string         `db:"deleted_by_username" json:"deleted_by_username"`
	DeletedByName     string         `db:"deleted_by_name" json:"deleted_by_name"`
	DeletedAt         time.Time      `db:"deleted_at" json:"deleted_at"`
	Type              string         `db:"type" json:"type"`
	Optin             string         `db:"optin" json:"optin"`
	Status            string         `db:"status" json:"status"`
	Dynamic           bool           `db:"dynamic" json:"dynamic"`
	Tags              types.JSONText `db:"tags" json:"tags"`

	// ExpiresAt and DaysLeft are filled by the core from the retention period.
	ExpiresAt time.Time `db:"-" json:"expires_at"`
	DaysLeft  int       `db:"-" json:"days_left"`
}

// TrashedList is what deleting a list into the trash reports for each list.
type TrashedList struct {
	ID       int    `db:"list_id" json:"id"`
	Name     string `db:"name" json:"name"`
	SubCount int    `db:"sub_count" json:"subscriber_count"`
}

// RestoredList says what a restore brought back.
type RestoredList struct {
	ID            int `json:"id"`
	Subscriptions int `json:"subscriptions"`
	Campaigns     int `json:"campaigns"`
}
