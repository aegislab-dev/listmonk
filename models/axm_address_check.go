package models

import "github.com/jmoiron/sqlx/types"

// AddressCheckRow is one subscriber of an audience whose address is checked.
type AddressCheckRow struct {
	ID    int    `db:"id"`
	Email string `db:"email"`
	// Direct: subscribed to one of the chosen ordinary lists (not only in through a dynamic one).
	Direct bool `db:"direct"`
}

// AddressCheckList is a list by ID and name.
type AddressCheckList struct {
	ID   int    `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
}

// AddressCheckDynamic is a dynamic list with its rule.
type AddressCheckDynamic struct {
	ID     int            `db:"id"`
	Filter types.JSONText `db:"axm_filter"`
}
