package models

// AddressCheckRow is one subscriber of an audience whose address is checked.
type AddressCheckRow struct {
	ID    int    `db:"id"`
	Email string `db:"email"`
}
