package models

import "time"

// ListCampaignUse is one campaign that sent to (or is sending to, or is scheduled for) a
// list (LST-63). Name is empty for a user who may not read campaigns. Sent is the whole
// campaign's count; ListsN says how many lists it targets. Total is how many such
// campaigns the list has, of which a response carries the newest few.
type ListCampaignUse struct {
	ListID     int        `db:"list_id" json:"list_id"`
	CampaignID int        `db:"campaign_id" json:"campaign_id"`
	Name       string     `db:"name" json:"name"`
	Status     string     `db:"status" json:"status"`
	Sent       int        `db:"sent" json:"sent"`
	ToSend     int        `db:"to_send" json:"to_send"`
	StartedAt  *time.Time `db:"started_at" json:"started_at"`
	SendAt     *time.Time `db:"send_at" json:"send_at"`
	UpdatedAt  time.Time  `db:"updated_at" json:"updated_at"`
	ListsN     int        `db:"lists_n" json:"lists_n"`
	Total      int        `db:"total" json:"total"`
}

// ListUsePerList is how many campaigns of a list the response carries.
const ListUsePerList = 5
