package models

import "gopkg.in/volatiletech/null.v6"

// SenderAddress is a globally verified reply address. Token hashes are never serialized.
type SenderAddress struct {
	ID             int         `db:"id" json:"id"`
	Email          string      `db:"email" json:"email"`
	Status         string      `db:"status" json:"status"`
	TokenHash      null.String `db:"token_hash" json:"-"`
	TokenExpiresAt null.Time   `db:"token_expires_at" json:"-"`
	RequestedBy    null.Int    `db:"requested_by" json:"requested_by"`
	RequesterName  string      `db:"requester_name" json:"requester_name"`
	VerifiedAt     null.Time   `db:"verified_at" json:"verified_at"`
	LastSentAt     null.Time   `db:"last_sent_at" json:"last_sent_at"`
	CreatedAt      null.Time   `db:"created_at" json:"created_at"`
	UpdatedAt      null.Time   `db:"updated_at" json:"updated_at"`
}
